package service

import (
	"regexp"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// client_visits.go records browser interactions with a client's
// subscription page: every open of the page (with the visitor's IP and
// a device label parsed from the User-Agent), every granted location
// report, and — the point of the feature — every DENIED location
// prompt. A subscriber who declines used to leave no trace; now the
// open and the refusal are recorded, so the panel shows which IP and
// device opened the page and that location sharing was refused there.
// Only browser page opens create rows; client apps fetching the raw
// subscription never touch the HTML page and are never recorded here.

const (
	subVisitOutcomeOpened  = "opened"
	subVisitOutcomeGranted = "granted"
	subVisitOutcomeDenied  = "denied"

	// Newest visits kept per client; older opens have no value.
	subVisitKeepPerEmail = 30
	// Rows served to the panel UI.
	subVisitDisplayCap = 10
)

// RecordClientSubVisitBySubId stores one page interaction for every
// distinct client email that owns the subscription id (normally one).
func RecordClientSubVisitBySubId(subId, ip, device, outcome string) error {
	if outcome != subVisitOutcomeOpened && outcome != subVisitOutcomeGranted && outcome != subVisitOutcomeDenied {
		return nil
	}
	db := database.GetDB()
	var emails []string
	if err := db.Model(&model.ClientRecord{}).
		Where("sub_id = ?", subId).
		Distinct().
		Pluck("email", &emails).Error; err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, email := range emails {
		if email == "" {
			continue
		}
		row := model.ClientSubVisit{
			Email: email, SubID: subId, IP: ip, Device: device,
			Outcome: outcome, CreatedAt: now,
		}
		if err := db.Create(&row).Error; err != nil {
			return err
		}
		var stale []int
		db.Model(&model.ClientSubVisit{}).
			Where("email = ?", email).
			Order("created_at DESC, id DESC").
			Offset(subVisitKeepPerEmail).
			Pluck("id", &stale)
		if len(stale) > 0 {
			db.Where("id IN ?", stale).Delete(&model.ClientSubVisit{})
		}
	}
	return nil
}

// ClientSubVisitInfo is one recorded page interaction, panel-shaped.
type ClientSubVisitInfo struct {
	IP      string `json:"ip"`
	Device  string `json:"device"`
	Outcome string `json:"outcome"`
	Time    string `json:"time"`
}

// GetClientSubVisits returns a client's newest recorded subscription
// page interactions, newest first.
func GetClientSubVisits(email string) ([]ClientSubVisitInfo, error) {
	db := database.GetDB()
	var rows []model.ClientSubVisit
	if err := db.Where("email = ?", email).
		Order("created_at DESC, id DESC").
		Limit(subVisitDisplayCap).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ClientSubVisitInfo, 0, len(rows))
	for _, r := range rows {
		info := ClientSubVisitInfo{IP: r.IP, Device: r.Device, Outcome: r.Outcome}
		if r.CreatedAt > 0 {
			info.Time = time.Unix(r.CreatedAt, 0).Local().Format("2006-01-02 15:04:05")
		}
		out = append(out, info)
	}
	return out, nil
}

var (
	androidUARe = regexp.MustCompile(`Android ([\d.]+); ([^)]+)\)`)
	iosUARe     = regexp.MustCompile(`(iPhone|iPad|iPod).*?OS ([\d_]+)`)
	windowsUARe = regexp.MustCompile(`Windows NT ([\d.]+)`)
	macUARe     = regexp.MustCompile(`Mac OS X ([\d_]+)`)
)

// ParseDeviceLabel turns a browser User-Agent into a short human label
// like "SM-S918B · Android 14 · Chrome" or "iPhone · iOS 17.2 · Safari".
// It is a display aid parsed from what the browser itself declares —
// good enough to tell a subscriber's phone from a stranger's, not a
// device fingerprint.
func ParseDeviceLabel(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return ""
	}
	browser := ""
	switch {
	case strings.Contains(ua, "SamsungBrowser/"):
		browser = "Samsung Browser"
	case strings.Contains(ua, "Edg/") || strings.Contains(ua, "EdgiOS/"):
		browser = "Edge"
	case strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "FxiOS/") || strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	join := func(parts ...string) string {
		kept := make([]string, 0, len(parts))
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		return strings.Join(kept, " · ")
	}
	if m := androidUARe.FindStringSubmatch(ua); m != nil {
		model := strings.TrimSpace(m[2])
		// The segment can carry extra fields ("SM-S918B Build/UP1A..."):
		// the model is the part before any ';' or ' Build'.
		if i := strings.Index(model, ";"); i >= 0 {
			model = strings.TrimSpace(model[:i])
		}
		if i := strings.Index(model, " Build"); i >= 0 {
			model = strings.TrimSpace(model[:i])
		}
		if strings.EqualFold(model, "K") || model == "" {
			model = "Android"
			return join(model, m[1], browser)
		}
		return join(model, "Android "+m[1], browser)
	}
	if m := iosUARe.FindStringSubmatch(ua); m != nil {
		return join(m[1], "iOS "+strings.ReplaceAll(m[2], "_", "."), browser)
	}
	if m := windowsUARe.FindStringSubmatch(ua); m != nil {
		return join("Windows "+m[1], browser)
	}
	if m := macUARe.FindStringSubmatch(ua); m != nil {
		return join("macOS "+strings.ReplaceAll(m[1], "_", "."), browser)
	}
	if strings.Contains(ua, "Linux") {
		return join("Linux", browser)
	}
	if len(ua) > 60 {
		ua = ua[:60]
	}
	return ua
}
