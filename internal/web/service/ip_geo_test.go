package service

import (
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestLookupIpGeo_CacheAndPrivate(t *testing.T) {
	setupClientIpTestDB(t)
	db := database.GetDB()

	// Seed a fresh cache entry: the lookup must serve it without any
	// provider call.
	if err := db.Create(&model.IpGeoCache{
		IP: "9.9.9.9", Lat: 35.6892, Lon: 51.389, City: "Tehran",
		Country: "Iran", Found: true, UpdatedAt: time.Now().Unix(),
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
		IP: "8.8.4.4", Found: false, UpdatedAt: time.Now().Unix(),
	}).Error; err != nil {
		t.Fatalf("seed negative cache: %v", err)
	}
	if g := lookupIpGeo("8.8.4.4"); g != nil {
		t.Fatalf("negative cache should return nil, got %+v", g)
	}
}
