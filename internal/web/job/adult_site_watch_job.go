package job

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/tgbot"
)

// adultSiteEvent gates this watcher in the Telegram enabled-events list,
// the same opt-in pattern as share.watch: it fires only while
// "adult.site" is present in tgEnabledEvents.
const adultSiteEvent = "adult.site"

// adultVisitGapSeconds separates two visits to the same site. While a
// client is on a site, its record's lastSeen advances every few seconds
// (each new connection the access log folds in); that whole stretch is
// ONE visit and alerts once, the moment it starts. When activity on the
// site stops and resumes after a gap larger than this, that is a new
// entry — a new visit — and it alerts again, immediately. There is no
// cooldown: the gap only tells one continuous visit apart from the next
// entry, it never suppresses a new entry.
const adultVisitGapSeconds = 120

// AdultSiteWatchJob watches the per-client visited-site records (the
// same data the panel's visited-sites section shows) and sends a
// Telegram alert the moment a client enters an adult (18+) site — see
// service.AdultSiteIdentity for how a host is judged. It scans the
// shared records of every client, so every client is covered from
// their first recorded visit on; a newly created client needs no
// registration and gets a watcher automatically. It runs wherever the
// panel's Telegram bot runs (the master): the site table there holds
// every location's merged records, so one watcher covers all
// locations. Nothing is enforced; the operator judges.
type AdultSiteWatchJob struct {
	tgbotService tgbot.Tgbot
	seeded       bool
	seen         map[string]int64 // guid|email|host -> lastSeen watermark
}

// NewAdultSiteWatchJob creates a new adult-site watch job instance.
func NewAdultSiteWatchJob() *AdultSiteWatchJob {
	return &AdultSiteWatchJob{
		seen: map[string]int64{},
	}
}

type adultSiteHit struct {
	Email string
	Guid  string
	Site  string // the full host exactly as visited (e.g. www.xvideos.com)
	When  int64  // unix seconds of the visit, from the access log
}

// Run performs one watch pass and alerts on clients entering adult sites.
func (j *AdultSiteWatchJob) Run() {
	if !j.tgbotService.IsRunning() {
		return
	}
	events, err := (&service.SettingService{}).GetTgEnabledEvents()
	if err != nil {
		return
	}
	enabled := false
	for e := range strings.SplitSeq(events, ",") {
		if strings.TrimSpace(e) == adultSiteEvent {
			enabled = true
			break
		}
	}
	if !enabled {
		return
	}

	db := database.GetDB()
	var rows []model.NodeClientSite
	if err := db.Find(&rows).Error; err != nil {
		logger.Warning("[AdultSiteWatch] read site records failed: ", err)
		return
	}

	hits := j.scan(rows)
	if len(hits) == 0 {
		return
	}
	loc, err := (&service.SettingService{}).GetTimeLocation()
	if err != nil || loc == nil {
		loc = time.Local
	}
	labels := service.LocationLabels()
	byUser := map[string][]adultSiteHit{}
	order := []string{}
	for _, h := range hits {
		key := h.Email + "|" + h.Guid
		if _, ok := byUser[key]; !ok {
			order = append(order, key)
		}
		byUser[key] = append(byUser[key], h)
	}
	sort.Strings(order)
	for _, key := range order {
		group := byUser[key]
		j.tgbotService.NotifyAdmins(adultAlertText(group[0].Email, labels[group[0].Guid], group, loc))
	}
}

// scan folds one pass over the site records into the job's watermarks
// and returns the alert-worthy hits: a client ENTERING an adult site.
// The first pass after a start only seeds the watermarks (baseline):
// sites visited before the watcher started are history, not news, and
// must not burst-alert on boot. After that, a host alerts when it is
// first seen for the client, or when its lastSeen resumes after a gap
// larger than adultVisitGapSeconds (a new entry); continuous activity
// inside one visit only moves the watermark.
func (j *AdultSiteWatchJob) scan(rows []model.NodeClientSite) []adultSiteHit {
	var hits []adultSiteHit
	for _, row := range rows {
		var entries []model.ClientSiteEntry
		if err := json.Unmarshal([]byte(row.Sites), &entries); err != nil {
			continue
		}
		for _, e := range entries {
			key := row.NodeGuid + "|" + row.Email + "|" + e.Host
			prev := j.seen[key]
			if e.LastSeen <= prev {
				continue
			}
			j.seen[key] = e.LastSeen
			if !j.seeded {
				continue
			}
			if _, ok := service.AdultSiteIdentity(e.Host); !ok {
				continue
			}
			if prev != 0 && e.LastSeen-prev <= adultVisitGapSeconds {
				continue // same continuous visit, already alerted at entry
			}
			hits = append(hits, adultSiteHit{Email: row.Email, Guid: row.NodeGuid, Site: e.Host, When: e.LastSeen})
		}
	}
	j.seeded = true
	return hits
}

func adultAlertText(email, location string, hits []adultSiteHit, loc *time.Location) string {
	if location == "" {
		location = "?"
	}
	if loc == nil {
		loc = time.Local
	}
	sorted := make([]adultSiteHit, len(hits))
	copy(sorted, hits)
	sort.Slice(sorted, func(a, b int) bool {
		if sorted[a].When != sorted[b].When {
			return sorted[a].When < sorted[b].When
		}
		return sorted[a].Site < sorted[b].Site
	})
	when := func(ts int64) string {
		return time.Unix(ts, 0).In(loc).Format("15:04:05 — 2006-01-02")
	}

	var b strings.Builder
	b.WriteString("🔞 <b>بازدید از سایت بزرگسالان (۱۸+)</b>\n")
	fmt.Fprintf(&b, "👤 کاربر: <b>%s</b>\n", email)
	fmt.Fprintf(&b, "🌍 لوکیشن: %s\n", location)
	if len(sorted) == 1 {
		fmt.Fprintf(&b, "🔗 سایت: %s\n", sorted[0].Site)
		fmt.Fprintf(&b, "🕐 زمان: %s", when(sorted[0].When))
	} else {
		b.WriteString("🔗 سایت‌ها:\n")
		for _, h := range sorted {
			fmt.Fprintf(&b, "• %s — 🕐 %s\n", h.Site, when(h.When))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
