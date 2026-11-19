package service

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"gorm.io/gorm/clause"
)

// client_sites.go turns the local Xray access log into per-client
// destination statistics: which hosts (domains, or IPs when the client
// sent an address) each client's connections went to, how many accepted
// connections each host saw, and when the last one happened.
//
// The access log only exists when the Xray template sets log.access to a
// file (the bundled default points at /etc/x-ui/access.log). Each line
// looks like:
//
//	2026/11/19 10:48:12.547361 from 203.0.113.7:51234 accepted tcp:example.com:443 [vless-in >> direct] email: alice
//
// Only the destination host and the user email are used. Byte counts per
// site are intentionally absent: Xray accounts traffic per user in
// total, never per destination, so such a number cannot exist.

const (
	// Site entries older than this are pruned on merge: the panel shows
	// recent behaviour, not a lifetime dossier.
	clientSiteRetentionSeconds = 60 * 24 * 3600
	// Per client and node, at most this many hosts are kept (top by count).
	clientSiteMaxPerEmail = 200
	// The merged list served to the panel UI.
	clientSiteDisplayCap = 50
	// Once the access log grows past this and has been fully consumed,
	// the collector truncates it (copytruncate-style; Xray appends).
	accessLogMaxBytes = 64 << 20
	// Per collector cycle, at most this many log bytes are consumed.
	accessLogReadCap = 32 << 20
)

var accessLogLineRe = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2})(?:\.\d+)? from \S+ accepted (?:tcp|udp):(.+) \[.*\] email: (\S+)\s*$`)

// parseAccessLogLine extracts (email, host, unixTime) from one Xray
// access-log line. ok=false for anything that is not an accepted
// connection line with a user email (rejections, DNS, other protocols).
func parseAccessLogLine(line string) (email, host string, ts int64, ok bool) {
	m := accessLogLineRe.FindStringSubmatch(line)
	if m == nil {
		return "", "", 0, false
	}
	dest := m[2]
	// dest is host:port; strip the trailing port. IPv6 literals arrive
	// bracketed ([2001:db8::1]:443) — keep the literal, drop brackets.
	if i := strings.LastIndex(dest, ":"); i > 0 {
		dest = dest[:i]
	}
	host = strings.Trim(dest, "[]")
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	email = m[3]
	if host == "" || email == "" {
		return "", "", 0, false
	}
	if t, err := time.ParseInLocation("2006/01/02 15:04:05", m[1], time.Local); err == nil {
		ts = t.Unix()
	}
	return email, host, ts, true
}

// accessLogPath returns the access log of the RUNNING Xray config. The
// panel confines the template's log.access to its log folder when it
// generates the runtime config (resolveXrayLogPaths keeps only the base
// name), so the template value is NOT the file Xray writes — the
// generated config is the only truthful source, via xray.GetAccessLogPath.
// "" means access logging is off.
func (s *InboundService) accessLogPath() string {
	p, err := xray.GetAccessLogPath()
	if err != nil {
		return ""
	}
	p = strings.TrimSpace(p)
	if p == "" || strings.EqualFold(p, "none") {
		return ""
	}
	return p
}

type accessLogState struct {
	Offset int64 `json:"offset"`
}

// CollectClientSites consumes the not-yet-read tail of the Xray access
// log and folds it into this panel's own NodeClientSite rows (under the
// local panel guid, exactly like local IP observations). It is cheap and
// safe to call on a timer: no new log data means no database writes.
func (s *InboundService) CollectClientSites() {
	defer func() {
		if r := recover(); r != nil {
			logger.Warning("[ClientSites] collect panicked:", r)
		}
	}()
	path := s.accessLogPath()
	if path == "" {
		return
	}
	statePath := path + ".parsestate"
	var st accessLogState
	if raw, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	f, err := os.Open(path)
	if err != nil {
		return // log file not created yet (no Xray run since the template change)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return
	}
	if fi.Size() < st.Offset {
		st.Offset = 0 // truncated or rotated underneath us: start over
	}
	if _, err := f.Seek(st.Offset, io.SeekStart); err != nil {
		return
	}
	now := time.Now().Unix()
	perEmail := make(map[string]map[string]*model.ClientSiteEntry)
	consumed := int64(0)
	reader := bufio.NewReaderSize(f, 64*1024)
	for consumed < accessLogReadCap {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			if !strings.HasSuffix(line, "\n") {
				// Partial tail line (Xray is mid-write): leave it for the
				// next cycle by not advancing past it.
				break
			}
			consumed += int64(len(line))
			if email, host, ts, ok := parseAccessLogLine(strings.TrimRight(line, "\r\n")); ok {
				if ts == 0 {
					ts = now
				}
				hosts := perEmail[email]
				if hosts == nil {
					hosts = make(map[string]*model.ClientSiteEntry)
					perEmail[email] = hosts
				}
				e := hosts[host]
				if e == nil {
					e = &model.ClientSiteEntry{Host: host}
					hosts[host] = e
				}
				e.Count++
				if ts > e.LastSeen {
					e.LastSeen = ts
				}
			}
		}
		if err != nil {
			break
		}
	}
	st.Offset += consumed
	if raw, err := json.Marshal(st); err == nil {
		_ = os.WriteFile(statePath, raw, 0o644)
	}
	// Rotate: a fully consumed oversized log is truncated so the volume
	// does not fill with access history nobody reads twice.
	if fi.Size() > accessLogMaxBytes && st.Offset >= fi.Size() {
		if err := os.Truncate(path, 0); err == nil {
			st.Offset = 0
			if raw, merr := json.Marshal(st); merr == nil {
				_ = os.WriteFile(statePath, raw, 0o644)
			}
		}
	}
	if len(perEmail) == 0 {
		return
	}
	localGuid, _ := (&SettingService{}).GetPanelGuid()
	if localGuid == "" {
		return
	}
	flat := make(map[string][]model.ClientSiteEntry, len(perEmail))
	for email, hosts := range perEmail {
		entries := make([]model.ClientSiteEntry, 0, len(hosts))
		for _, e := range hosts {
			entries = append(entries, *e)
		}
		flat[email] = entries
	}
	if err := upsertNodeClientSites(localGuid, flat); err != nil {
		logger.Warning("[ClientSites] store local observations failed:", err)
	}
}

// mergeClientSiteEntries folds incoming observations into old ones:
// counts add up, last-seen takes the max, entries past the retention
// window drop out, and the result is capped to the busiest hosts.
func mergeClientSiteEntries(old, incoming []model.ClientSiteEntry, cutoff int64) []model.ClientSiteEntry {
	byHost := make(map[string]*model.ClientSiteEntry, len(old)+len(incoming))
	add := func(e model.ClientSiteEntry, sum bool) {
		if e.Host == "" || e.LastSeen < cutoff {
			return
		}
		cur := byHost[e.Host]
		if cur == nil {
			cp := e
			byHost[e.Host] = &cp
			return
		}
		if sum {
			cur.Count += e.Count
		} else if e.Count > cur.Count {
			cur.Count = e.Count
		}
		if e.LastSeen > cur.LastSeen {
			cur.LastSeen = e.LastSeen
		}
	}
	for _, e := range old {
		add(e, false)
	}
	for _, e := range incoming {
		add(e, true)
	}
	out := make([]model.ClientSiteEntry, 0, len(byHost))
	for _, e := range byHost {
		out = append(out, *e)
	}
	model.SortClientSiteEntries(out)
	if len(out) > clientSiteMaxPerEmail {
		out = out[:clientSiteMaxPerEmail]
	}
	return out
}

// upsertNodeClientSites merges fresh observations per (guid, email),
// following the same discipline as upsertNodeClientIps.
func upsertNodeClientSites(guid string, perEmail map[string][]model.ClientSiteEntry) error {
	if guid == "" || len(perEmail) == 0 {
		return nil
	}
	db := database.GetDB()
	cutoff := time.Now().Unix() - clientSiteRetentionSeconds

	var existing []model.NodeClientSite
	if err := db.Where("node_guid = ?", guid).Find(&existing).Error; err != nil {
		return err
	}
	existingByEmail := make(map[string]*model.NodeClientSite, len(existing))
	for i := range existing {
		existingByEmail[existing[i].Email] = &existing[i]
	}

	emails := make([]string, 0, len(perEmail))
	for email := range perEmail {
		if email != "" {
			emails = append(emails, email)
		}
	}
	sort.Strings(emails)

	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()
	for _, email := range emails {
		var old []model.ClientSiteEntry
		if cur, ok := existingByEmail[email]; ok && cur.Sites != "" {
			_ = json.Unmarshal([]byte(cur.Sites), &old)
		}
		merged := mergeClientSiteEntries(old, perEmail[email], cutoff)
		if len(merged) == 0 {
			if _, ok := existingByEmail[email]; ok {
				if err := tx.Where("node_guid = ? AND email = ?", guid, email).
					Delete(&model.NodeClientSite{}).Error; err != nil {
					tx.Rollback()
					return err
				}
			}
			continue
		}
		encoded, _ := model.MarshalClientSiteEntries(merged)
		if current := existingByEmail[email]; current != nil && current.Sites == encoded {
			continue
		}
		row := model.NodeClientSite{NodeGuid: guid, Email: email, Sites: encoded}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "node_guid"}, {Name: "email"}},
			DoUpdates: clause.AssignmentColumns([]string{"sites"}),
		}).Create(&row).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit().Error
}

// GetClientSitesByGuid returns this panel's full site-attribution subtree
// (guid -> email -> entries). It is what the clientSitesByGuid endpoint
// serves to a parent panel.
func (s *InboundService) GetClientSitesByGuid() (map[string]map[string][]model.ClientSiteEntry, error) {
	db := database.GetDB()
	var rows []model.NodeClientSite
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	cutoff := time.Now().Unix() - clientSiteRetentionSeconds
	out := make(map[string]map[string][]model.ClientSiteEntry)
	for _, row := range rows {
		if row.NodeGuid == "" || row.Email == "" || row.Sites == "" {
			continue
		}
		var entries []model.ClientSiteEntry
		if err := json.Unmarshal([]byte(row.Sites), &entries); err != nil {
			continue
		}
		fresh := mergeClientSiteEntries(nil, entries, cutoff)
		if len(fresh) == 0 {
			continue
		}
		if out[row.NodeGuid] == nil {
			out[row.NodeGuid] = make(map[string][]model.ClientSiteEntry)
		}
		out[row.NodeGuid][row.Email] = fresh
	}
	return out, nil
}

// MergeClientSitesByGuid folds a node's guid-keyed site report into the
// local table, with the same clone-remapping as MergeClientIpsByGuid.
// Counts from a node are cumulative snapshots of its own aggregation, so
// a re-merge REPLACES the node's previous entries for an email instead of
// adding to them (the collector's own local guid is the only writer that
// ever sums).
func (s *InboundService) MergeClientSitesByGuid(node *model.Node, trees map[string]map[string][]model.ClientSiteEntry) error {
	if node != nil && node.Guid != "" {
		if eff := effectiveNodeKey(node); eff != node.Guid {
			if sub, ok := trees[node.Guid]; ok {
				delete(trees, node.Guid)
				if existing, ok := trees[eff]; ok {
					for email, sites := range sub {
						existing[email] = append(existing[email], sites...)
					}
				} else {
					trees[eff] = sub
				}
			}
		}
	}
	localGuid, _ := (&SettingService{}).GetPanelGuid()
	for guid, perEmail := range trees {
		if guid == localGuid {
			continue // never overwrite our own observations with a pushed copy
		}
		if err := replaceNodeClientSites(guid, perEmail); err != nil {
			return err
		}
	}
	return nil
}

// replaceNodeClientSites stores a node's latest snapshot verbatim.
func replaceNodeClientSites(guid string, perEmail map[string][]model.ClientSiteEntry) error {
	if guid == "" || len(perEmail) == 0 {
		return nil
	}
	db := database.GetDB()
	cutoff := time.Now().Unix() - clientSiteRetentionSeconds
	emails := make([]string, 0, len(perEmail))
	for email := range perEmail {
		if email != "" {
			emails = append(emails, email)
		}
	}
	sort.Strings(emails)
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()
	for _, email := range emails {
		merged := mergeClientSiteEntries(nil, perEmail[email], cutoff)
		if len(merged) == 0 {
			if err := tx.Where("node_guid = ? AND email = ?", guid, email).
				Delete(&model.NodeClientSite{}).Error; err != nil {
				tx.Rollback()
				return err
			}
			continue
		}
		encoded, _ := model.MarshalClientSiteEntries(merged)
		row := model.NodeClientSite{NodeGuid: guid, Email: email, Sites: encoded}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "node_guid"}, {Name: "email"}},
			DoUpdates: clause.AssignmentColumns([]string{"sites"}),
		}).Create(&row).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit().Error
}

// ClientSiteInfo is one row of the panel's per-client visited-sites list.
// Service is the known-service label for the host (see site_service.go),
// "" for generic hosts — the destination is all the log ever carries,
// never which app on the device opened the connection.
type ClientSiteInfo struct {
	Host     string `json:"host"`
	Service  string `json:"service"`
	Count    int64  `json:"count"`
	LastSeen string `json:"lastSeen"`
}

// GetClientSitesWithNodes merges a client's site entries across every
// node that reported them (this panel included), busiest first.
func (s *InboundService) GetClientSitesWithNodes(email string) ([]ClientSiteInfo, error) {
	db := database.GetDB()
	var rows []model.NodeClientSite
	if err := db.Where("email = ?", email).Find(&rows).Error; err != nil {
		return nil, err
	}
	cutoff := time.Now().Unix() - clientSiteRetentionSeconds
	byHost := make(map[string]*model.ClientSiteEntry)
	for _, row := range rows {
		if row.Sites == "" {
			continue
		}
		var entries []model.ClientSiteEntry
		if err := json.Unmarshal([]byte(row.Sites), &entries); err != nil {
			continue
		}
		for _, e := range entries {
			if e.Host == "" || e.LastSeen < cutoff {
				continue
			}
			cur := byHost[e.Host]
			if cur == nil {
				cp := e
				byHost[e.Host] = &cp
				continue
			}
			cur.Count += e.Count
			if e.LastSeen > cur.LastSeen {
				cur.LastSeen = e.LastSeen
			}
		}
	}
	merged := make([]model.ClientSiteEntry, 0, len(byHost))
	for _, e := range byHost {
		merged = append(merged, *e)
	}
	model.SortClientSiteEntries(merged)
	if len(merged) > clientSiteDisplayCap {
		merged = merged[:clientSiteDisplayCap]
	}
	out := make([]ClientSiteInfo, 0, len(merged))
	for _, e := range merged {
		info := ClientSiteInfo{Host: e.Host, Service: classifySiteHost(e.Host), Count: e.Count}
		if e.LastSeen > 0 {
			info.LastSeen = time.Unix(e.LastSeen, 0).Local().Format("2006-01-02 15:04:05")
		}
		out = append(out, info)
	}
	return out, nil
}
