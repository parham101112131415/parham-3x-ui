package model

// ClientSubVisit is one recorded browser interaction with a client's
// subscription page (the /sub/<subId> info page or the /loc check page):
// the page was opened, the browser location prompt was answered, or it
// was denied. A denial used to leave no trace at all; now the open and
// the denial are themselves the signal — the panel sees which IP and
// device opened the page and whether location sharing was refused.
// The VPN connection itself still reveals none of this; only a browser
// page open creates a row.
type ClientSubVisit struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Email     string `json:"email" gorm:"index;not null"`
	SubID     string `json:"subId" gorm:"index;not null"`
	IP        string `json:"ip"`     // source IP of the page/report request
	Device    string `json:"device"` // short label parsed from the browser User-Agent
	Outcome   string `json:"outcome" gorm:"index"` // opened | granted | denied
	CreatedAt int64  `json:"createdAt" gorm:"index"` // unix seconds
}
