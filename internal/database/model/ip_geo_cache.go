package model

// IpGeoCache caches the approximate geolocation of one public IP address
// (city-level, as reported by the upstream geo provider). It exists so the
// panel resolves each distinct client IP at most once in a while instead of
// calling the provider for every info-dialog open: the coordinates are a
// display aid that lets the admin tell whether a client's new IP still comes
// from roughly the same place (e.g. an airplane-mode reconnect) or from a
// different location entirely (possible account sharing).
type IpGeoCache struct {
	IP        string  `json:"ip" gorm:"primaryKey"`
	Lat       float64 `json:"lat" gorm:"default:0"`
	Lon       float64 `json:"lon" gorm:"default:0"`
	City      string  `json:"city" gorm:"default:''"`
	Country   string  `json:"country" gorm:"default:''"`
	Found     bool    `json:"found" gorm:"default:false"` // false = provider had no location for this IP
	UpdatedAt int64   `json:"updatedAt" gorm:"default:0"` // unix seconds
}
