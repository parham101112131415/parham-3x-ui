package model

import (
	"cmp"
	"encoding/json"
	"slices"
)

// ClientSiteEntry aggregates one destination host a client connected to:
// how many accepted connections the local Xray access log recorded and
// when the most recent one happened. Hosts are destination domains (or
// IPs, when the client sent an address instead of a name) — never full
// URLs: TLS content is encrypted and the proxy only ever sees the host it
// connects to. There is deliberately no byte count: Xray accounts traffic
// per user in total, not per destination, so per-site bytes do not exist.
type ClientSiteEntry struct {
	Host     string `json:"host"`
	Count    int64  `json:"count"`
	LastSeen int64  `json:"lastSeen"` // unix seconds
}

// SortClientSiteEntries orders by count descending, then most recent
// first, then host — the panel's "most visited sites" order.
func SortClientSiteEntries(entries []ClientSiteEntry) {
	slices.SortFunc(entries, func(a, b ClientSiteEntry) int {
		if order := cmp.Compare(b.Count, a.Count); order != 0 {
			return order
		}
		if order := cmp.Compare(b.LastSeen, a.LastSeen); order != 0 {
			return order
		}
		return cmp.Compare(a.Host, b.Host)
	})
}

func MarshalClientSiteEntries(entries []ClientSiteEntry) (string, error) {
	if len(entries) == 0 {
		return "[]", nil
	}
	SortClientSiteEntries(entries)
	encoded, err := json.Marshal(entries)
	return string(encoded), err
}

// NodeClientSite records, per panel (panelGuid), the destination hosts a
// client's connections went to on that panel's own Xray, aggregated from
// its access log. It mirrors NodeClientIp's attribution discipline: each
// panel writes only its own observations under its own guid, and the
// master merges every node's guid-keyed report — never mixing in data a
// parent pushed down.
type NodeClientSite struct {
	Id       int    `json:"id" gorm:"primaryKey;autoIncrement"`
	NodeGuid string `json:"nodeGuid" gorm:"uniqueIndex:idx_nodesite_guid_email,priority:1;not null"`
	Email    string `json:"email" gorm:"uniqueIndex:idx_nodesite_guid_email,priority:2;not null"`
	Sites    string `json:"sites"`
}
