package model

// ClientIpHistory keeps the most recent IPs a client was seen connecting
// from, attributed to the panel (NodeGuid) that observed them. Unlike the
// live IP tables (InboundClientIps, NodeClientIp), whose entries expire
// after 30 minutes so IP-limit counting stays fresh, this history is
// persistent: it answers "which IP is this client connecting from, and
// what was the last IP it used" even after the client has been offline
// for a long time. Only the newest few IPs per client are retained (see
// the service layer); rows are removed when the client is deleted or its
// IP log is cleared.
type ClientIpHistory struct {
	Id       int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Email    string `json:"email" gorm:"uniqueIndex:idx_iphist_email_ip,priority:1;not null"`
	IP       string `json:"ip" gorm:"uniqueIndex:idx_iphist_email_ip,priority:2;not null"`
	NodeGuid string `json:"nodeGuid" gorm:"default:''"`
	LastSeen int64  `json:"lastSeen" gorm:"default:0"` // unix seconds
}
