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
// it was used on: this panel's own client_traffics row first, then one row
// per node from node_client_traffics (the per-node figures the master keeps
// for its aggregate), labelled with the node names.
func (s *InboundService) GetClientUsageByLocation(email string) ([]ClientLocationUsage, error) {
	db := database.GetDB()
	out := make([]ClientLocationUsage, 0, 4)

	var local xray.ClientTraffic
	err := db.Where("email = ?", email).First(&local).Error
	switch {
	case err == nil:
		name := ""
		if local.InboundId > 0 {
			var ib model.Inbound
			if e := db.Select("remark").Where("id = ?", local.InboundId).First(&ib).Error; e == nil {
				name = ib.Remark
			}
		}
		out = append(out, ClientLocationUsage{Node: name, Up: local.Up, Down: local.Down})
	case errors.Is(err, gorm.ErrRecordNotFound):
		// No local usage recorded; node rows may still exist.
	default:
		return nil, err
	}

	var rows []model.NodeClientTraffic
	if err := db.Where("email = ?", email).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		ids := make([]int, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.NodeId)
		}
		var nodes []model.Node
		if err := db.Where("id IN ?", ids).Find(&nodes).Error; err != nil {
			return nil, err
		}
		nameById := make(map[int]string, len(nodes))
		for _, n := range nodes {
			nameById[n.Id] = n.Name
		}
		sort.Slice(rows, func(i, j int) bool {
			return nameById[rows[i].NodeId] < nameById[rows[j].NodeId]
		})
		for _, r := range rows {
			out = append(out, ClientLocationUsage{Node: nameById[r.NodeId], Up: r.Up, Down: r.Down})
		}
	}
	return out, nil
}
