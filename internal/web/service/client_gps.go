package service

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// client_gps.go stores and serves the browser-geolocation reports a
// subscriber's own browser sends from the /loc/<subId> connection-check
// page (see internal/sub/loc.go). The coordinates are the device's own
// fix at page-open time — the only true device location the panel can
// ever hold, and only because the subscriber's browser asked them to
// share it.

// ClientSubIdExists reports whether any client record carries this
// subscription id (the /loc page 404s for unknown ids, like /sub does).
func ClientSubIdExists(subId string) bool {
	if subId == "" {
		return false
	}
	db := database.GetDB()
	if db == nil {
		return false
	}
	var count int64
	if err := db.Model(&model.ClientRecord{}).Where("sub_id = ?", subId).Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}

// RecordClientGpsBySubId stores one browser location fix for every
// distinct client email that owns the subscription id (normally one).
// Only the newest few reports per client are kept.
func RecordClientGpsBySubId(subId string, lat, lon, accuracyM float64, ip string) error {
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
		row := model.ClientGpsReport{
			Email: email, SubID: subId, Lat: lat, Lon: lon,
			AccuracyM: accuracyM, IP: ip, CreatedAt: now,
		}
		if err := db.Create(&row).Error; err != nil {
			return err
		}
		// Keep the newest 20 reports per client; older fixes have no
		// operational value and the table should not grow without bound.
		var stale []int
		db.Model(&model.ClientGpsReport{}).
			Where("email = ?", email).
			Order("created_at DESC, id DESC").
			Offset(20).
			Pluck("id", &stale)
		if len(stale) > 0 {
			db.Where("id IN ?", stale).Delete(&model.ClientGpsReport{})
		}
	}
	return nil
}

// ClientGpsInfo is one stored browser fix, formatted for the panel.
type ClientGpsInfo struct {
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	AccuracyM float64 `json:"accuracyM"`
	IP        string  `json:"ip"`
	Time      string  `json:"time"`
}

// GetClientGpsReports returns a client's newest browser location fixes
// (at most 5), newest first. An empty result simply means the subscriber
// never opened the check page (or never shared their location there).
func GetClientGpsReports(email string) ([]ClientGpsInfo, error) {
	db := database.GetDB()
	var rows []model.ClientGpsReport
	if err := db.Where("email = ?", email).
		Order("created_at DESC, id DESC").
		Limit(5).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ClientGpsInfo, 0, len(rows))
	for _, r := range rows {
		info := ClientGpsInfo{Lat: r.Lat, Lon: r.Lon, AccuracyM: r.AccuracyM, IP: r.IP}
		if r.CreatedAt > 0 {
			info.Time = time.Unix(r.CreatedAt, 0).Local().Format("2006-01-02 15:04:05")
		}
		out = append(out, info)
	}
	return out, nil
}
