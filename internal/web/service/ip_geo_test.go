package service

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// TestLookupMmdbGeo runs only when PARHAM_GEOLITE2_CITY_DB points at a real
// GeoLite2 City database; it pins the two behaviours the merge logic relies
// on: a city-level record with a small radius, and a country-level Iranian
// mobile block whose radius honestly spans the country.
func TestLookupMmdbGeo(t *testing.T) {
	if os.Getenv("PARHAM_GEOLITE2_CITY_DB") == "" {
		t.Skip("PARHAM_GEOLITE2_CITY_DB not set")
	}
	info, radius, has := lookupMmdbGeo(net.ParseIP("5.52.10.20"))
	if !has || info == nil {
		t.Fatalf("expected a record for 5.52.10.20")
	}
	if info.City != "Tehran" || radius == 0 || info.RadiusKm != radius {
		t.Fatalf("unexpected city record: %+v radius=%d", info, radius)
	}
	_, radius2, has2 := lookupMmdbGeo(net.ParseIP("77.77.126.169"))
	if !has2 || radius2 < 100 {
		t.Fatalf("expected a coarse record for 77.77.126.169, has=%v radius=%d", has2, radius2)
	}
}

func TestLookupIpGeo_CacheAndPrivate(t *testing.T) {
	setupClientIpTestDB(t)
	db := database.GetDB()

	// Seed a fresh cache entry: the lookup must serve it without any
	// provider call.
	if err := db.Create(&model.IpGeoCache{
		IP: "9.9.9.9", Lat: 35.6892, Lon: 51.389, City: "Tehran",
		Country: "Iran", RadiusKm: 25, Found: true, V: ipGeoCacheVersion,
		UpdatedAt: time.Now().Unix(),
	}).Error; err != nil {
		t.Fatalf("seed geo cache: %v", err)
	}
	geo := lookupIpGeo("9.9.9.9")
	if geo == nil {
		t.Fatalf("expected cached geo, got nil")
	}
	if geo.City != "Tehran" || geo.Country != "Iran" || geo.Lat != 35.6892 || geo.Lon != 51.389 {
		t.Fatalf("unexpected cached geo: %+v", geo)
	}
	if geo.RadiusKm != 25 {
		t.Fatalf("expected cached radius 25, got %+v", geo)
	}

	// Private / invalid addresses never resolve.
	if g := lookupIpGeo("192.168.1.10"); g != nil {
		t.Fatalf("private IP should not resolve, got %+v", g)
	}
	if g := lookupIpGeo("127.0.0.1"); g != nil {
		t.Fatalf("loopback should not resolve, got %+v", g)
	}
	if g := lookupIpGeo("not-an-ip"); g != nil {
		t.Fatalf("invalid IP should not resolve, got %+v", g)
	}

	// A fresh "not found" cache entry suppresses provider retries.
	if err := db.Create(&model.IpGeoCache{
		IP: "8.8.4.4", Found: false, V: ipGeoCacheVersion, UpdatedAt: time.Now().Unix(),
	}).Error; err != nil {
		t.Fatalf("seed negative cache: %v", err)
	}
	if g := lookupIpGeo("8.8.4.4"); g != nil {
		t.Fatalf("negative cache should return nil, got %+v", g)
	}
}
