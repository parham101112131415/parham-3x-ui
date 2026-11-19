package tgbot

import (
	"fmt"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// parhamFullReport renders the complete panel report appended to the daily
// stats message: per-location period traffic, who is online and where, top
// consumers, new clients, near-limit clients, live sharing suspects and node
// health. Sections with nothing to say are omitted.
func (t *Tgbot) parhamFullReport() string {
	data, err := (&service.ParhamReportService{}).Build()
	if err != nil || data == nil {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📊 <b>گزارش کامل پنل</b> (%.0f ساعت گذشته)\n", data.PeriodHours)

	b.WriteString("\n🌍 <b>مصرف لوکیشن‌ها:</b>\n")
	for _, loc := range data.Locations {
		fmt.Fprintf(&b, "• %s: %s (آنلاین: %d)\n",
			loc.Label, common.FormatTraffic(loc.UpPeriod+loc.DownPeriod), loc.Online)
	}
	fmt.Fprintf(&b, "📥 <b>مجموع:</b> ⬇️ %s ⬆️ %s\n",
		common.FormatTraffic(data.TotalDown), common.FormatTraffic(data.TotalUp))

	fmt.Fprintf(&b, "\n👥 <b>آنلاین الان (%d):</b>\n", len(data.OnlineNow))
	if len(data.OnlineNow) == 0 {
		b.WriteString("—\n")
	}
	for _, c := range data.OnlineNow {
		fmt.Fprintf(&b, "• %s — %s\n", c.Email, strings.Join(c.Locations, "، "))
	}

	if len(data.TopToday) > 0 {
		b.WriteString("\n🏆 <b>پرمصرف‌ترین‌ها:</b>\n")
		for _, c := range data.TopToday {
			fmt.Fprintf(&b, "• %s: %s\n", c.Email, common.FormatTraffic(c.Bytes))
		}
	}

	if len(data.NewClients) > 0 {
		fmt.Fprintf(&b, "\n🆕 <b>کلاینت‌های جدید:</b> %s\n", strings.Join(data.NewClients, "، "))
	}

	if len(data.NearLimit) > 0 {
		b.WriteString("\n⚠️ <b>نزدیک اتمام:</b>\n")
		for _, c := range data.NearLimit {
			fmt.Fprintf(&b, "• %s — %s\n", c.Email, c.Reason)
		}
	}

	if len(data.Suspects) > 0 {
		b.WriteString("\n🚨 <b>مشکوک به اشتراک‌گذاری (همین الان):</b>\n")
		for _, s := range data.Suspects {
			fmt.Fprintf(&b, "• %s — %d آی‌پی — %s\n",
				s.Email, len(s.IPs), strings.Join(s.Locations, "، "))
		}
	}

	if len(data.Nodes) > 0 {
		b.WriteString("\n🖥 <b>نودها:</b> ")
		parts := make([]string, 0, len(data.Nodes))
		for _, n := range data.Nodes {
			if n.Online {
				parts = append(parts, n.Name+" ✅")
			} else if n.OfflineSecs > 0 {
				parts = append(parts, fmt.Sprintf("%s 🔴 (%s)", n.Name, agoText(n.OfflineSecs)))
			} else {
				parts = append(parts, n.Name+" 🔴")
			}
		}
		b.WriteString(strings.Join(parts, " • "))
	}

	return strings.TrimRight(b.String(), "\n")
}

// agoText renders a compact "time since" for node offline durations.
func agoText(secs int64) string {
	switch {
	case secs < 3600:
		return fmt.Sprintf("%dm", secs/60)
	case secs < 86400:
		return fmt.Sprintf("%dh %dm", secs/3600, (secs%3600)/60)
	default:
		return fmt.Sprintf("%dd %dh", secs/86400, (secs%86400)/3600)
	}
}
