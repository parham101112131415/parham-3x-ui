package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"

	"github.com/oschwald/geoip2-golang"
	"gorm.io/gorm"
)

// IpGeoInfo is the approximate location of one client IP as shown in the
// panel: city and country plus the coordinates, so two IPs of the same
// client can be compared at a glance. The location is IP-based (the ISP's
// egress area), never a device GPS position. RadiusKm, when known, is
// MaxMind's accuracy radius for the IP block: the point should be read as
// "somewhere within this many km of these coordinates".
type IpGeoInfo struct {
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	City     string  `json:"city"`
	Country  string  `json:"country"`
	RadiusKm int     `json:"radiusKm,omitempty"`
}

// How long a cached geolocation stays fresh. A found location is rechecked
// monthly (IP blocks rarely move); a "not found" answer is retried daily.
const (
	ipGeoFoundTTL    = 30 * 24 * time.Hour
	ipGeoNotFoundTTL = 24 * time.Hour
	ipGeoHTTPTimeout = 3 * time.Second
)

// ipGeoCacheVersion marks rows written by the current merge logic (local
// MaxMind GeoLite2 City + ip-api). Older rows are refreshed once so the
// accuracy radius appears without waiting out the 30-day TTL.
const ipGeoCacheVersion = 2

// lookupIpGeo resolves one IP to its approximate location, using the
// persistent cache first and the geo sources on a miss. Best-effort by
// design: any failure returns nil and never blocks or fails the caller —
// the IP itself is still displayed, just without coordinates.
// LookupIpGeoLabel returns a short "City, Country" label for an IP, or ""
// when no location is known. Used by notification texts outside this package.
func LookupIpGeoLabel(ip string) string {
	info := lookupIpGeo(ip)
	if info == nil {
		return ""
	}
	label := info.City
	if info.Country != "" {
		if label != "" {
			label += ", "
		}
		label += info.Country
	}
	return label
}

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
		if row.V == ipGeoCacheVersion {
			age := time.Duration(now-row.UpdatedAt) * time.Second
			if row.Found && age < ipGeoFoundTTL {
				return &IpGeoInfo{Lat: row.Lat, Lon: row.Lon, City: row.City, Country: row.Country, RadiusKm: row.RadiusKm}
			}
			if !row.Found && age < ipGeoNotFoundTTL {
				return nil
			}
		}
		// Stale or pre-radius entry: fall through and refresh it.
	case errors.Is(err, gorm.ErrRecordNotFound):
		// Cache miss: fall through to the sources.
	default:
		logger.Warning("[IpGeo] cache read failed:", err)
		return nil
	}

	info := resolveIpGeo(parsed)
	fresh := model.IpGeoCache{IP: ip, UpdatedAt: now, V: ipGeoCacheVersion}
	if info != nil {
		fresh.Lat, fresh.Lon, fresh.City, fresh.Country, fresh.RadiusKm, fresh.Found =
			info.Lat, info.Lon, info.City, info.Country, info.RadiusKm, true
	}
	if err == nil {
		if uerr := db.Model(&model.IpGeoCache{}).Where("ip = ?", ip).Updates(map[string]any{
			"lat": fresh.Lat, "lon": fresh.Lon, "city": fresh.City,
			"country": fresh.Country, "radius_km": fresh.RadiusKm,
			"found": fresh.Found, "v": fresh.V, "updated_at": fresh.UpdatedAt,
		}).Error; uerr != nil {
			logger.Warning("[IpGeo] cache update failed:", uerr)
		}
	} else if cerr := db.Create(&fresh).Error; cerr != nil {
		logger.Warning("[IpGeo] cache insert failed:", cerr)
	}
	return info
}

// resolveIpGeo merges the available geo sources for one IP:
//
//   - The local MaxMind GeoLite2 City database is the only source that
//     reports an accuracy radius, and its per-block splits are the finest
//     available offline — when it names a city, its point wins.
//   - GeoLite2's Iran coverage is thin, though: many Iranian blocks have
//     no city at all (country-level point, radius up to 1000 km). For
//     those, ip-api.com usually still names a city, so its point is used
//     and MaxMind's radius is kept alongside as the honest uncertainty
//     bound for the block.
//   - With no city anywhere, a country-level MaxMind record still beats
//     nothing; with no record anywhere, the result is nil.
func resolveIpGeo(parsed net.IP) *IpGeoInfo {
	mm, mmRadius, mmHasRecord := lookupMmdbGeo(parsed)
	if mm != nil && mm.City != "" {
		return mm
	}
	if api := fetchIpGeo(parsed.String()); api != nil {
		if mmHasRecord {
			api.RadiusKm = mmRadius
		}
		return api
	}
	if mmHasRecord {
		return mm
	}
	return nil
}

// --- Local MaxMind GeoLite2 City database -------------------------------

var (
	mmdbOnce   sync.Once
	mmdbReader *geoip2.Reader
)

// mmdbPath finds the GeoLite2 City database: explicit env override first,
// then the path baked into the Docker image, then a file next to the
// working directory (local development).
func mmdbPath() string {
	if p := os.Getenv("PARHAM_GEOLITE2_CITY_DB"); p != "" {
		return p
	}
	for _, p := range []string{
		"/usr/local/share/geoip/GeoLite2-City.mmdb",
		"GeoLite2-City.mmdb",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// openMmdb lazily opens the database once. A missing database is not an
// error: lookups simply fall back to the online provider.
func openMmdb() *geoip2.Reader {
	mmdbOnce.Do(func() {
		path := mmdbPath()
		if path == "" {
			return
		}
		r, err := geoip2.Open(path)
		if err != nil {
			logger.Warning("[IpGeo] GeoLite2 open failed:", err)
			return
		}
		mmdbReader = r
	})
	return mmdbReader
}

// lookupMmdbGeo reads one IP from the local GeoLite2 City database. The
// second return value is the block's accuracy radius in km (0 when the
// record carries none); the third reports whether the database has any
// record for the IP at all.
func lookupMmdbGeo(parsed net.IP) (info *IpGeoInfo, radiusKm int, hasRecord bool) {
	r := openMmdb()
	if r == nil {
		return nil, 0, false
	}
	rec, err := r.City(parsed)
	if err != nil || rec == nil {
		return nil, 0, false
	}
	country := rec.Country.Names["en"]
	city := rec.City.Names["en"]
	if country == "" && city == "" && rec.Location.Latitude == 0 && rec.Location.Longitude == 0 {
		return nil, 0, false
	}
	return &IpGeoInfo{
		Lat:      rec.Location.Latitude,
		Lon:      rec.Location.Longitude,
		City:     city,
		Country:  country,
		RadiusKm: int(rec.Location.AccuracyRadius),
	}, int(rec.Location.AccuracyRadius), true
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
