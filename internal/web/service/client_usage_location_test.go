package service

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// Regression test for the duplicated-location bug: the central
// client_traffics row holds the cross-location TOTAL (node deltas are
// folded into it) and its inbound_id can point at a node inbound. The old
// code labelled that row with that inbound's remark, so a client showed
// e.g. two "eu-west" rows (the total wearing eu-west's name, plus the real
// eu-west row) and no local row at all. The figures below are a real
// client's live numbers from when the bug was reported.
func TestGetClientUsageByLocation_SplitsTotalByLocation(t *testing.T) {
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
	db := database.GetDB()

	euWest := 2
	must := func(err error) {
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// Local inbound 1 ("us-west", node_id NULL) and the eu-west node
	// inbound 3 — the central row's inbound_id points at 3, as in the bug.
	must(db.Create(&model.Inbound{Id: 1, Remark: "us-west", Port: 8081, Protocol: "vless", Tag: "in-us-west"}).Error)
	must(db.Create(&model.Inbound{Id: 3, Remark: "eu-west", Port: 8081, Protocol: "vless", Tag: "in-eu-west", NodeID: &euWest}).Error)
	must(db.Create(&model.ClientRecord{Id: 1, Email: "erfan2", Enable: true}).Error)
	must(db.Create(&model.ClientInbound{ClientId: 1, InboundId: 1}).Error)
	must(db.Create(&model.ClientInbound{ClientId: 1, InboundId: 3}).Error)

	must(db.Create(&model.Node{Id: 1, Name: "us-east"}).Error)
	must(db.Create(&model.Node{Id: 2, Name: "eu-west"}).Error)
	must(db.Create(&model.Node{Id: 3, Name: "asia"}).Error)

	// Central row = total across all four locations.
	must(db.Create(&xray.ClientTraffic{
		Email: "erfan2", InboundId: 3, Up: 4195626, Down: 65289227,
	}).Error)
	// Per-node rows.
	must(db.Create(&model.NodeClientTraffic{NodeId: 1, Email: "erfan2", Up: 3338, Down: 8178}).Error)
	must(db.Create(&model.NodeClientTraffic{NodeId: 2, Email: "erfan2", Up: 4185394, Down: 65264486}).Error)
	must(db.Create(&model.NodeClientTraffic{NodeId: 3, Email: "erfan2", Up: 3338, Down: 8180}).Error)

	svc := &InboundService{}
	rows, err := svc.GetClientUsageByLocation("erfan2")
	if err != nil {
		t.Fatalf("GetClientUsageByLocation: %v", err)
	}
	want := []ClientLocationUsage{
		{Node: "us-west", Up: 3556, Down: 8383}, // total − nodes, local label
		{Node: "asia", Up: 3338, Down: 8180},
		{Node: "eu-west", Up: 4185394, Down: 65264486},
		{Node: "us-east", Up: 3338, Down: 8178},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows %v, want %d", len(rows), rows, len(want))
	}
	for i, w := range want {
		if rows[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}
}
