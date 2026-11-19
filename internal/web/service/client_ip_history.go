package service

import (
	"errors"
	"sort"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"gorm.io/gorm"
)

// clientIpHistoryKeep is how many of a client's most recent IPs are retained
// in the persistent history.
const clientIpHistoryKeep = 5

// recordClientIpHistory folds one panel's fresh IP observations (keyed by the
// observing panel's guid) into the persistent per-client IP history. It is
// called after the live attribution upsert commits, for both local scanner
// observations and node reports merged from children, so the master ends up
// holding the history of the whole fleet.
//
// Best-effort by design: the history is a display aid, so failures are logged
// and never propagated — the IP scan and node sync must not fail because of it.
func recordClientIpHistory(nodeGuid string, perEmail map[string][]model.ClientIpEntry) {
	defer func() {
		if r := recover(); r != nil {
			logger.Warning("[IpHistory] record panicked:", r)
		}
	}()
	db := database.GetDB()
	for email, entries := range perEmail {
		if email == "" || len(entries) == 0 {
			continue
		}
		for _, e := range entries {
			if e.IP == "" || e.Timestamp <= 0 {
				continue
			}
			var existing model.ClientIpHistory
			err := db.Where("email = ? AND ip = ?", email, e.IP).First(&existing).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				row := model.ClientIpHistory{Email: email, IP: e.IP, NodeGuid: nodeGuid, LastSeen: e.Timestamp}
				if cerr := db.Create(&row).Error; cerr != nil {
					logger.Warning("[IpHistory] insert failed:", cerr)
				}
			case err != nil:
				logger.Warning("[IpHistory] read failed:", err)
			case e.Timestamp > existing.LastSeen:
				if uerr := db.Model(&model.ClientIpHistory{}).
					Where("id = ?", existing.Id).
					Updates(map[string]any{"node_guid": nodeGuid, "last_seen": e.Timestamp}).Error; uerr != nil {
					logger.Warning("[IpHistory] update failed:", uerr)
				}
			}
		}
		trimClientIpHistory(db, email)
	}
}

// trimClientIpHistory drops all but the newest clientIpHistoryKeep IPs of a
// client, so the table stays a "last few addresses" log, not a full archive.
func trimClientIpHistory(db *gorm.DB, email string) {
	var rows []model.ClientIpHistory
	if err := db.Where("email = ?", email).
		Order("last_seen DESC, ip ASC").
		Find(&rows).Error; err != nil {
		logger.Warning("[IpHistory] trim read failed:", err)
		return
	}
	if len(rows) <= clientIpHistoryKeep {
		return
	}
	ids := make([]int, 0, len(rows)-clientIpHistoryKeep)
	for _, r := range rows[clientIpHistoryKeep:] {
		ids = append(ids, r.Id)
	}
	if err := db.Where("id IN ?", ids).Delete(&model.ClientIpHistory{}).Error; err != nil {
		logger.Warning("[IpHistory] trim delete failed:", err)
	}
}

// ClientIpHistoryInfo is one persistent-history entry shown in the panel: the
// IP, when it was last seen, and the name of the node it connected through
// ("" = this local panel).
type ClientIpHistoryInfo struct {
	IP   string     `json:"ip"`
	Time string     `json:"time"`
	Node string     `json:"node"`
	Geo  *IpGeoInfo `json:"geo,omitempty"`
}

// GetClientIpHistory returns a client's persistent IP history, newest first,
// at most clientIpHistoryKeep entries, with node names resolved the same way
// as GetClientIpsWithNodes.
func (s *InboundService) GetClientIpHistory(email string) ([]ClientIpHistoryInfo, error) {
	db := database.GetDB()
	var rows []model.ClientIpHistory
	if err := db.Where("email = ?", email).
		Order("last_seen DESC, ip ASC").
		Limit(clientIpHistoryKeep).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	guidName := s.nodeGuidNameMap()
	localGuid, _ := (&SettingService{}).GetPanelGuid()
	out := make([]ClientIpHistoryInfo, 0, len(rows))
	for _, r := range rows {
		info := ClientIpHistoryInfo{IP: r.IP}
		if r.LastSeen > 0 {
			info.Time = time.Unix(r.LastSeen, 0).Local().Format("2006-01-02 15:04:05")
		}
		if r.NodeGuid != "" && r.NodeGuid != localGuid {
			info.Node = guidName[r.NodeGuid]
		}
		info.Geo = lookupIpGeo(r.IP)
		out = append(out, info)
	}
	return out, nil
}

// ClientLocationUsage is one row of a client's per-location traffic: the
// location (node) display name and the upload/download recorded there.
// The local panel's row carries the remark of the client's local inbound
// ("" when it cannot be resolved).
type ClientLocationUsage struct {
	Node string `json:"node"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

// GetClientUsageByLocation splits a client's total traffic by the location
// it was used on: one row per node from node_client_traffics (the per-node
// figures the master keeps), plus this panel's own row.
//
// The central client_traffics row is NOT the local figure — node sync folds
// every node's deltas into it, so it holds the client's cross-location
// total, and its inbound_id points at whichever inbound last touched it.
// Labelling that row with that inbound's remark (the old behaviour) showed
// the total under a random node's name, duplicating that node and hiding
// the local location entirely. The local figure is what remains of the
// total after the per-node rows are subtracted.
func (s *InboundService) GetClientUsageByLocation(email string) ([]ClientLocationUsage, error) {
	db := database.GetDB()

	var rows []model.NodeClientTraffic
	if err := db.Where("email = ?", email).Find(&rows).Error; err != nil {
		return nil, err
	}
	nameById := make(map[int]string, len(rows))
	var nodeUp, nodeDown int64
	if len(rows) > 0 {
		ids := make([]int, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.NodeId)
			nodeUp += r.Up
			nodeDown += r.Down
		}
		var nodes []model.Node
		if err := db.Where("id IN ?", ids).Find(&nodes).Error; err != nil {
			return nil, err
		}
		for _, n := range nodes {
			nameById[n.Id] = n.Name
		}
	}

	out := make([]ClientLocationUsage, 0, len(rows)+1)

	var total xray.ClientTraffic
	err := db.Where("email = ?", email).First(&total).Error
	switch {
	case err == nil:
		localUp := total.Up - nodeUp
		if localUp < 0 {
			localUp = 0
		}
		localDown := total.Down - nodeDown
		if localDown < 0 {
			localDown = 0
		}
		out = append(out, ClientLocationUsage{
			Node: s.localUsageLabel(db, email, total.InboundId),
			Up:   localUp,
			Down: localDown,
		})
	case errors.Is(err, gorm.ErrRecordNotFound):
		// No central row; node rows may still exist.
	default:
		return nil, err
	}

	sort.Slice(rows, func(i, j int) bool {
		return nameById[rows[i].NodeId] < nameById[rows[j].NodeId]
	})
	for _, r := range rows {
		out = append(out, ClientLocationUsage{Node: nameById[r.NodeId], Up: r.Up, Down: r.Down})
	}
	return out, nil
}

// localUsageLabel names the local row of GetClientUsageByLocation: the
// remark of a local inbound (node_id NULL) the client is attached to,
// falling back to the central row's inbound when that one is local. ""
// when no local inbound can be resolved.
func (s *InboundService) localUsageLabel(db *gorm.DB, email string, centralInboundId int) string {
	var attached []int
	if err := db.Table("client_inbounds").
		Select("client_inbounds.inbound_id").
		Joins("JOIN clients ON clients.id = client_inbounds.client_id").
		Where("clients.email = ?", email).
		Scan(&attached).Error; err == nil {
		sort.Ints(attached)
		for _, id := range attached {
			if remark, ok := localInboundRemark(db, id); ok {
				return remark
			}
		}
	}
	if remark, ok := localInboundRemark(db, centralInboundId); ok {
		return remark
	}
	return ""
}

func localInboundRemark(db *gorm.DB, inboundId int) (string, bool) {
	if inboundId <= 0 {
		return "", false
	}
	var ib model.Inbound
	if err := db.Select("remark", "node_id").Where("id = ?", inboundId).First(&ib).Error; err != nil {
		return "", false
	}
	if ib.NodeID != nil {
		return "", false
	}
	return ib.Remark, true
}
