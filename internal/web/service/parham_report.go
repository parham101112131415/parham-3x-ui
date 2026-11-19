package service

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// AttributedIp is one live IP sighting for a client, tagged with the guid of
// the panel instance (location) it was seen on.
type AttributedIp struct {
	IP   string
	Guid string
}

// FoldLiveClientIps folds per-guid IP attribution rows into per-client lists
// of IPs sighted at or after cutoff (unix seconds), deduped by IP.
func FoldLiveClientIps(rows []model.NodeClientIp, cutoff int64) map[string][]AttributedIp {
	out := map[string][]AttributedIp{}
	seen := map[string]map[string]bool{}
	for _, row := range rows {
		var entries []model.ClientIpEntry
		if err := json.Unmarshal([]byte(row.Ips), &entries); err != nil {
			continue
		}
		for _, e := range entries {
			if e.Timestamp < cutoff || e.IP == "" {
				continue
			}
			if seen[row.Email] == nil {
				seen[row.Email] = map[string]bool{}
			}
			if seen[row.Email][e.IP] {
				continue
			}
			seen[row.Email][e.IP] = true
			out[row.Email] = append(out[row.Email], AttributedIp{IP: e.IP, Guid: row.NodeGuid})
		}
	}
	return out
}

// LocationLabels maps an attribution/online guid to a display name: the
// node's name for registered nodes, and this panel's own first local inbound
// remark for the local guid.
func LocationLabels() map[string]string {
	labels := map[string]string{}
	db := database.GetDB()
	var nodes []model.Node
	if err := db.Find(&nodes).Error; err == nil {
		for _, n := range nodes {
			if n.Guid != "" {
				labels[n.Guid] = n.Name
			}
		}
	}
	if guid, err := (&SettingService{}).GetPanelGuid(); err == nil && guid != "" {
		label := "Local"
		var inbound model.Inbound
		if err := db.Where("node_id IS NULL").Order("id").First(&inbound).Error; err == nil && inbound.Remark != "" {
			label = inbound.Remark
		}
		labels[guid] = label
	}
	return labels
}

// ParhamLocationStat is one location's traffic for the report.
type ParhamLocationStat struct {
	Label     string
	UpPeriod  int64
	DownPeriod int64
	Online    int
}

// ParhamOnlineClient is one currently-online client and where it is connected.
type ParhamOnlineClient struct {
	Email     string
	Locations []string
}

// ParhamClientUsage is one client's traffic over the report period.
type ParhamClientUsage struct {
	Email string
	Bytes int64
}

// ParhamNearLimit flags a client close to its traffic cap or expiry.
type ParhamNearLimit struct {
	Email  string
	Reason string
}

// ParhamSuspect is a client live from 2+ IPs at once at report time.
type ParhamSuspect struct {
	Email     string
	IPs       []string
	Locations []string
}

// ParhamNodeState is one node's health for the report.
type ParhamNodeState struct {
	Name         string
	Online       bool
	OfflineSecs  int64
}

// ParhamReportData is everything the full daily report renders.
type ParhamReportData struct {
	PeriodHours float64
	Locations   []ParhamLocationStat
	TotalUp     int64
	TotalDown   int64
	OnlineNow   []ParhamOnlineClient
	TopToday    []ParhamClientUsage
	NewClients  []string
	NearLimit   []ParhamNearLimit
	Suspects    []ParhamSuspect
	Nodes       []ParhamNodeState
}

type parhamTotals struct {
	Clients   map[string][2]int64 `json:"clients"`
	Locations map[string][2]int64 `json:"locations"`
}

// ParhamReportService assembles the full daily report data.
type ParhamReportService struct {
	inboundService InboundService
}

// Build computes the report data, diffing cumulative totals against the
// previous report's snapshot (kept in the DB), then stores a fresh snapshot.
func (s *ParhamReportService) Build() (*ParhamReportData, error) {
	db := database.GetDB()
	now := time.Now()
	nowUnix := now.Unix()

	// Cumulative per-client totals: local Xray counters + node sums.
	clientTotals := map[string][2]int64{}
	var localRows []struct {
		Email string
		Up    int64
		Down  int64
	}
	if err := db.Table("client_traffics").
		Select("email, SUM(up) AS up, SUM(down) AS down").
		Group("email").Scan(&localRows).Error; err != nil {
		return nil, err
	}
	for _, r := range localRows {
		cur := clientTotals[r.Email]
		clientTotals[r.Email] = [2]int64{cur[0] + r.Up, cur[1] + r.Down}
	}
	nodeTotals, err := s.inboundService.GetNodeClientTrafficTotals()
	if err != nil {
		return nil, err
	}
	for email, ud := range nodeTotals {
		cur := clientTotals[email]
		clientTotals[email] = [2]int64{cur[0] + ud[0], cur[1] + ud[1]}
	}

	labels := LocationLabels()
	localGuid, _ := (&SettingService{}).GetPanelGuid()
	localLabel := labels[localGuid]
	if localLabel == "" {
		localLabel = "Local"
	}

	// Cumulative per-location totals, keyed by display label.
	locationTotals := map[string][2]int64{}
	var localSum struct {
		Up   int64
		Down int64
	}
	if err := db.Table("client_traffics").
		Select("COALESCE(SUM(up),0) AS up, COALESCE(SUM(down),0) AS down").
		Scan(&localSum).Error; err == nil {
		locationTotals[localLabel] = [2]int64{localSum.Up, localSum.Down}
	}
	nodeNameById := map[int]string{}
	var nodes []model.Node
	if err := db.Find(&nodes).Error; err != nil {
		return nil, err
	}
	for _, n := range nodes {
		nodeNameById[n.Id] = n.Name
	}
	var nodeLocRows []struct {
		NodeId int
		Up     int64
		Down   int64
	}
	if err := db.Table("node_client_traffics").
		Select("node_id, SUM(up) AS up, SUM(down) AS down").
		Group("node_id").Scan(&nodeLocRows).Error; err != nil {
		return nil, err
	}
	for _, r := range nodeLocRows {
		name := nodeNameById[r.NodeId]
		if name == "" {
			continue
		}
		cur := locationTotals[name]
		locationTotals[name] = [2]int64{cur[0] + r.Up, cur[1] + r.Down}
	}

	// Diff against the previous snapshot (within 36 h), then store a new one.
	prev, prevAt := s.loadSnapshot(nowUnix - 36*3600)
	periodHours := 24.0
	if prevAt > 0 {
		periodHours = now.Sub(time.Unix(prevAt, 0)).Hours()
	}
	diff := func(cur, old [2]int64) (int64, int64) {
		up := cur[0] - old[0]
		down := cur[1] - old[1]
		if up < 0 {
			up = cur[0]
		}
		if down < 0 {
			down = cur[1]
		}
		return up, down
	}
	data := &ParhamReportData{PeriodHours: periodHours}

	onlineByGuid := s.inboundService.GetOnlineClientsByGuid()
	onlineCountByLabel := map[string]int{}
	emailLocations := map[string]map[string]bool{}
	for guid, emails := range onlineByGuid {
		label := labels[guid]
		if label == "" {
			label = guid
		}
		onlineCountByLabel[label] = len(emails)
		for _, email := range emails {
			if emailLocations[email] == nil {
				emailLocations[email] = map[string]bool{}
			}
			emailLocations[email][label] = true
		}
	}

	locNames := make([]string, 0, len(locationTotals))
	for name := range locationTotals {
		locNames = append(locNames, name)
	}
	sort.Strings(locNames)
	for _, name := range locNames {
		cur := locationTotals[name]
		var old [2]int64
		if prev != nil {
			old = prev.Locations[name]
		}
		up, down := diff(cur, old)
		data.Locations = append(data.Locations, ParhamLocationStat{
			Label: name, UpPeriod: up, DownPeriod: down, Online: onlineCountByLabel[name],
		})
		data.TotalUp += up
		data.TotalDown += down
	}

	for email, locs := range emailLocations {
		names := make([]string, 0, len(locs))
		for name := range locs {
			names = append(names, name)
		}
		sort.Strings(names)
		data.OnlineNow = append(data.OnlineNow, ParhamOnlineClient{Email: email, Locations: names})
	}
	sort.Slice(data.OnlineNow, func(a, b int) bool { return data.OnlineNow[a].Email < data.OnlineNow[b].Email })

	periodByClient := []ParhamClientUsage{}
	for email, cur := range clientTotals {
		var old [2]int64
		if prev != nil {
			old = prev.Clients[email]
		}
		up, down := diff(cur, old)
		if up+down > 0 {
			periodByClient = append(periodByClient, ParhamClientUsage{Email: email, Bytes: up + down})
		}
	}
	sort.Slice(periodByClient, func(a, b int) bool { return periodByClient[a].Bytes > periodByClient[b].Bytes })
	if len(periodByClient) > 5 {
		periodByClient = periodByClient[:5]
	}
	data.TopToday = periodByClient

	// Client records: new-in-period + near-limit.
	var records []model.ClientRecord
	if err := db.Find(&records).Error; err != nil {
		return nil, err
	}
	sinceMs := now.Add(-time.Duration(periodHours * float64(time.Hour))).UnixMilli()
	for _, r := range records {
		if r.CreatedAt >= sinceMs {
			data.NewClients = append(data.NewClients, r.Email)
		}
		if !r.Enable {
			continue
		}
		if r.TotalGB > 0 {
			used := clientTotals[r.Email]
			if pct := (used[0] + used[1]) * 100 / r.TotalGB; pct >= 90 {
				data.NearLimit = append(data.NearLimit, ParhamNearLimit{
					Email: r.Email, Reason: percentReason(pct),
				})
				continue
			}
		}
		if r.ExpiryTime > 0 {
			daysLeft := (r.ExpiryTime - now.UnixMilli()) / 86400000
			if daysLeft >= 0 && daysLeft <= 3 {
				data.NearLimit = append(data.NearLimit, ParhamNearLimit{
					Email: r.Email, Reason: daysReason(daysLeft),
				})
			}
		}
	}
	sort.Strings(data.NewClients)

	// Sharing suspects right now (same 120 s live window as the watcher).
	var ipRows []model.NodeClientIp
	if err := db.Find(&ipRows).Error; err == nil {
		live := FoldLiveClientIps(ipRows, nowUnix-120)
		for email, ips := range live {
			if len(ips) < 2 {
				continue
			}
			suspect := ParhamSuspect{Email: email}
			locSet := map[string]bool{}
			for _, li := range ips {
				suspect.IPs = append(suspect.IPs, li.IP)
				if label := labels[li.Guid]; label != "" {
					locSet[label] = true
				}
			}
			for loc := range locSet {
				suspect.Locations = append(suspect.Locations, loc)
			}
			sort.Strings(suspect.Locations)
			sort.Strings(suspect.IPs)
			data.Suspects = append(data.Suspects, suspect)
		}
		sort.Slice(data.Suspects, func(a, b int) bool { return data.Suspects[a].Email < data.Suspects[b].Email })
	}

	for _, n := range nodes {
		state := ParhamNodeState{Name: n.Name, Online: n.Status == "online"}
		if !state.Online && n.LastHeartbeat > 0 {
			state.OfflineSecs = nowUnix - n.LastHeartbeat
		}
		data.Nodes = append(data.Nodes, state)
	}
	sort.Slice(data.Nodes, func(a, b int) bool { return data.Nodes[a].Name < data.Nodes[b].Name })

	s.storeSnapshot(nowUnix, &parhamTotals{Clients: clientTotals, Locations: locationTotals})
	return data, nil
}

func percentReason(pct int64) string {
	return "traffic " + strconv.FormatInt(pct, 10) + "%"
}

func daysReason(days int64) string {
	if days == 0 {
		return "expires today"
	}
	return "expires in " + strconv.FormatInt(days, 10) + "d"
}

func (s *ParhamReportService) loadSnapshot(afterUnix int64) (*parhamTotals, int64) {
	db := database.GetDB()
	var snap model.ParhamReportSnapshot
	if err := db.Where("taken_at >= ?", afterUnix).Order("taken_at DESC").First(&snap).Error; err != nil {
		return nil, 0
	}
	var totals parhamTotals
	if err := json.Unmarshal([]byte(snap.Data), &totals); err != nil {
		return nil, 0
	}
	return &totals, snap.TakenAt
}

func (s *ParhamReportService) storeSnapshot(nowUnix int64, totals *parhamTotals) {
	db := database.GetDB()
	raw, err := json.Marshal(totals)
	if err != nil {
		return
	}
	_ = db.Create(&model.ParhamReportSnapshot{TakenAt: nowUnix, Data: string(raw)}).Error
	_ = db.Where("taken_at < ?", nowUnix-72*3600).Delete(&model.ParhamReportSnapshot{}).Error
}
