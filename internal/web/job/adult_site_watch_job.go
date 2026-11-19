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

// adultAlertCooldown is the minimum gap between two alerts for the same
// client visiting the same site. Without it, a single browsing session
// (whose hosts reappear in the access log for as long as it lasts)
// would re-alert every minute.
const adultAlertCooldown = 6 * time.Hour

// AdultSiteWatchJob watches the per-client visited-site records (the
// same data the panel's visited-sites section shows) and sends ONE
// Telegram alert per client when a client is seen on an adult (18+)
// site — see service.AdultSiteIdentity for how a host is judged. It
// runs wherever the panel's Telegram bot runs (the master): the site
// table there holds every location's merged records, so one watcher
// covers all locations. Nothing is enforced; the operator judges.
type AdultSiteWatchJob struct {
	tgbotService tgbot.Tgbot
	seeded       bool
	seen         map[string]int64     // guid|email|host -> lastSeen watermark
	alerts       map[string]time.Time // email|site -> last alert time
}

// NewAdultSiteWatchJob creates a new adult-site watch job instance.
func NewAdultSiteWatchJob() *AdultSiteWatchJob {
	return &AdultSiteWatchJob{
		seen:   map[string]int64{},
		alerts: map[string]time.Time{},
	}
}

type adultSiteHit struct {
	Email string
	Guid  string
	Site  string
}

// Run performs one watch pass and alerts on newly-seen adult visits.
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

	hits := j.scan(rows, time.Now())
	if len(hits) == 0 {
		return
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
		j.tgbotService.NotifyAdmins(adultAlertText(group[0].Email, labels[group[0].Guid], group))
	}
}

// scan folds one pass over the site records into the job's watermarks
// and returns the alert-worthy hits. The first pass after a start only
// seeds the watermarks (baseline): sites visited before the watcher
// started are history, not news, and must not burst-alert on boot.
func (j *AdultSiteWatchJob) scan(rows []model.NodeClientSite, now time.Time) []adultSiteHit {
	var hits []adultSiteHit
	for _, row := range rows {
		var entries []model.ClientSiteEntry
		if err := json.Unmarshal([]byte(row.Sites), &entries); err != nil {
			continue
		}
		for _, e := range entries {
			key := row.NodeGuid + "|" + row.Email + "|" + e.Host
			if e.LastSeen <= j.seen[key] {
				continue
			}
			j.seen[key] = e.LastSeen
			if !j.seeded {
				continue
			}
			site, ok := service.AdultSiteIdentity(e.Host)
			if !ok {
				continue
			}
			alertKey := row.Email + "|" + site
			if now.Sub(j.alerts[alertKey]) < adultAlertCooldown {
				continue
			}
			j.alerts[alertKey] = now
			hits = append(hits, adultSiteHit{Email: row.Email, Guid: row.NodeGuid, Site: site})
		}
	}
	j.seeded = true
	return hits
}

func adultAlertText(email, location string, hits []adultSiteHit) string {
	if location == "" {
		location = "?"
	}
	sites := make([]string, 0, len(hits))
	for _, h := range hits {
		sites = append(sites, h.Site)
	}
	sort.Strings(sites)

	var b strings.Builder
	b.WriteString("🔞 <b>بازدید از سایت بزرگسالان (۱۸+)</b>\n")
	fmt.Fprintf(&b, "👤 کاربر: <b>%s</b>\n", email)
	fmt.Fprintf(&b, "🌍 لوکیشن: %s\n", location)
	if len(sites) == 1 {
		fmt.Fprintf(&b, "🔗 سایت: %s", sites[0])
	} else {
		b.WriteString("🔗 سایت‌ها:\n")
		for _, s := range sites {
			fmt.Fprintf(&b, "• %s\n", s)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
