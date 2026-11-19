package model

// ClientGpsReport is one browser-geolocation fix voluntarily sent by a
// subscriber's own browser from the /loc/<subId> connection-check page.
// Unlike the IP-based IpGeoCache (an ISP egress area), these coordinates
// come from the device itself (GPS/Wi-Fi positioning) at the moment the
// subscriber opened the page and accepted the browser's location prompt.
// They are point-in-time only: they say where the device was when the page
// was opened, nothing about where connections come from afterwards.
type ClientGpsReport struct {
	Id         int     `json:"id" gorm:"primaryKey;autoIncrement"`
	Email      string  `json:"email" gorm:"index;not null"`
	SubID      string  `json:"subId" gorm:"index;not null"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	AccuracyM  float64 `json:"accuracyM"` // browser-reported accuracy radius, metres
	IP         string  `json:"ip"`        // source IP of the report request
	CreatedAt  int64   `json:"createdAt" gorm:"index"` // unix seconds
}
