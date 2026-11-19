package job

import (
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

// shareLiveWindowSeconds is how fresh an attributed IP sighting must be to
// count as "connected right now". The local IP scan runs every 10 s and node
// attribution syncs on the node job's throttle, so two minutes is a safe
// simultaneity window without carrying stale sightings.
const shareLiveWindowSeconds = int64(120)

// ShareWatchJob watches for one client being live from two or more distinct
// IPs at the same time — the practical signature of a shared account — and
// sends ONE Telegram alert per episode to the panel admins. It runs on the
// master only (the panel that has nodes registered): the per-guid IP
// attribution table it reads is complete only there. The operator judges the
// alert; nothing is enforced automatically.
type ShareWatchJob struct {
	tgbotService tgbot.Tgbot
	alerted      map[string]bool
}

// NewShareWatchJob creates a new share-watch job instance.
func NewShareWatchJob() *ShareWatchJob {
	return &ShareWatchJob{alerted: map[string]bool{}}
}

// Run performs one share check and alerts on newly-shared clients.
func (j *ShareWatchJob) Run() {
	if !j.tgbotService.IsRunning() {
		return
	}
	db := database.GetDB()
	var nodeCount int64
	if err := db.Model(&model.Node{}).Count(&nodeCount).Error; err != nil || nodeCount == 0 {
		return // master-only: nodes hold a partial, local view
	}

	var rows []model.NodeClientIp
	if err := db.Find(&rows).Error; err != nil {
		logger.Warning("[ShareWatch] read attribution failed: ", err)
		return
	}
	live := service.FoldLiveClientIps(rows, time.Now().Unix()-shareLiveWindowSeconds)

	labels := service.LocationLabels()
	stillAlerted := map[string]bool{}
	for email, ips := range live {
		if len(ips) < 2 {
			continue
		}
		stillAlerted[email] = true
		if j.alerted[email] {
			continue
		}
		j.tgbotService.NotifyAdmins(shareAlertText(email, ips, labels))
	}
	j.alerted = stillAlerted
}

func shareAlertText(email string, ips []service.AttributedIp, labels map[string]string) string {
	locSet := map[string]bool{}
	for _, li := range ips {
		locSet[labels[li.Guid]] = true
	}
	locs := make([]string, 0, len(locSet))
	for loc := range locSet {
		if loc == "" {
			loc = "?"
		}
		locs = append(locs, loc)
	}
	sort.Strings(locs)

	sorted := append([]service.AttributedIp{}, ips...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].IP < sorted[b].IP })
	var b strings.Builder
	b.WriteString("🚨 <b>اشتراک‌گذاری هم‌زمان؟</b>\n")
	fmt.Fprintf(&b, "👤 کاربر: <b>%s</b>\n", email)
	fmt.Fprintf(&b, "🌍 لوکیشن‌ها: %s\n", strings.Join(locs, "، "))
	fmt.Fprintf(&b, "🔢 %d آی‌پی هم‌زمان:\n", len(sorted))
	for _, li := range sorted {
		geo := service.LookupIpGeoLabel(li.IP)
		loc := labels[li.Guid]
		parts := []string{li.IP}
		if geo != "" {
			parts = append(parts, geo)
		}
		if loc != "" {
			parts = append(parts, loc)
		}
		fmt.Fprintf(&b, "• %s\n", strings.Join(parts, " — "))
	}
	return strings.TrimRight(b.String(), "\n")
}
