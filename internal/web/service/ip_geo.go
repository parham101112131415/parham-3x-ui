package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"

	"gorm.io/gorm"
)

// IpGeoInfo is the approximate location of one client IP as shown in the
// panel: city and country plus the coordinates, so two IPs of the same
// client can be compared at a glance. The location is city-level (the
// ISP's egress point), never a device GPS position.
type IpGeoInfo struct {
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	City    string  `json:"city"`
	Country string  `json:"country"`
}

// How long a cached geolocation stays fresh. A found location is rechecked
// monthly (IP blocks rarely move); a "not found" answer is retried daily.
const (
	ipGeoFoundTTL    = 30 * 24 * time.Hour
	ipGeoNotFoundTTL = 24 * time.Hour
	ipGeoHTTPTimeout = 3 * time.Second
)

// lookupIpGeo resolves one IP to its approximate location, using the
// persistent cache first and the free ip-api.com provider on a miss.
// Best-effort by design: any failure returns nil and never blocks or fails
// the caller — the IP itself is still displayed, just without coordinates.
func lookupIpGeo(ip string) *IpGeoInfo {
	defer func() {
		if r := recover(); r != nil {
			logger.Warning("[IpGeo] lookup panicked:", r)
		}
	}()
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.IsPrivate() || parsed.IsLoopback() ||
		parsed.IsLinkLocalUnicast() || parsed.IsLinkLocalMulticast() ||
		parsed.IsMulticast() || parsed.IsUnspecified() {
		return nil
	}
	db := database.GetDB()
	if db == nil {
		return nil
	}
	now := time.Now().Unix()
	var row model.IpGeoCache
	err := db.Where("ip = ?", ip).First(&row).Error
	switch {
	case err == nil:
		age := time.Duration(now-row.UpdatedAt) * time.Second
		if row.Found && age < ipGeoFoundTTL {
			return &IpGeoInfo{Lat: row.Lat, Lon: row.Lon, City: row.City, Country: row.Country}
		}
		if !row.Found && age < ipGeoNotFoundTTL {
			return nil
		}
		// Stale entry: fall through and refresh it.
	case errors.Is(err, gorm.ErrRecordNotFound):
		// Cache miss: fall through to the provider.
	default:
		logger.Warning("[IpGeo] cache read failed:", err)
		return nil
	}

	info := fetchIpGeo(ip)
	fresh := model.IpGeoCache{IP: ip, UpdatedAt: now}
	if info != nil {
		fresh.Lat, fresh.Lon, fresh.City, fresh.Country, fresh.Found = info.Lat, info.Lon, info.City, info.Country, true
	}
	if err == nil {
		if uerr := db.Model(&model.IpGeoCache{}).Where("ip = ?", ip).Updates(map[string]any{
			"lat": fresh.Lat, "lon": fresh.Lon, "city": fresh.City,
			"country": fresh.Country, "found": fresh.Found, "updated_at": fresh.UpdatedAt,
		}).Error; uerr != nil {
			logger.Warning("[IpGeo] cache update failed:", uerr)
		}
	} else if cerr := db.Create(&fresh).Error; cerr != nil {
		logger.Warning("[IpGeo] cache insert failed:", cerr)
	}
	return info
}

// fetchIpGeo asks ip-api.com for one IP's city-level location. The free
// endpoint needs no key; a short timeout keeps an info-dialog open snappy
// even when the provider is unreachable.
func fetchIpGeo(ip string) *IpGeoInfo {
	client := &http.Client{Timeout: ipGeoHTTPTimeout}
	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,country,city,lat,lon", ip)
	resp, err := client.Get(url)
	if err != nil {
		logger.Warning("[IpGeo] provider request failed:", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logger.Warning("[IpGeo] provider status:", resp.Status)
		return nil
	}
	var body struct {
		Status  string  `json:"status"`
		Country string  `json:"country"`
		City    string  `json:"city"`
		Lat     float64 `json:"lat"`
		Lon     float64 `json:"lon"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		logger.Warning("[IpGeo] provider decode failed:", err)
		return nil
	}
	if body.Status != "success" {
		return nil
	}
	return &IpGeoInfo{Lat: body.Lat, Lon: body.Lon, City: body.City, Country: body.Country}
}
