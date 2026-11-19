package job

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func initNodeClientSpeedDB(t *testing.T) {
	t.Helper()
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
}

func setNodeClientTotal(t *testing.T, nodeID int, email string, up, down int64) {
	t.Helper()
	db := database.GetDB()
	row := model.NodeClientTraffic{NodeId: nodeID, Email: email, Up: up, Down: down}
	if err := db.Where("node_id = ? AND email = ?", nodeID, email).
		Assign(row).FirstOrCreate(&model.NodeClientTraffic{}).Error; err != nil {
		t.Fatalf("seed node client traffic: %v", err)
	}
}

func TestNodeClientSpeed_FirstCallIsBaselineOnly(t *testing.T) {
	initNodeClientSpeedDB(t)
	setNodeClientTotal(t, 1, "alice", 1000, 2000)

	j := &NodeTrafficSyncJob{inboundService: service.InboundService{}}
	if got := j.nodeClientSpeed(); len(got) != 0 {
		t.Fatalf("first call must only seed the baseline, got %v", got)
	}
}

func TestNodeClientSpeed_DeltaSumsAcrossNodes(t *testing.T) {
	initNodeClientSpeedDB(t)
	setNodeClientTotal(t, 1, "alice", 1000, 2000)
	setNodeClientTotal(t, 2, "alice", 500, 700)
	setNodeClientTotal(t, 1, "bob", 300, 300)

	j := &NodeTrafficSyncJob{inboundService: service.InboundService{}}
	_ = j.nodeClientSpeed() // baseline

	// alice moves on both nodes; bob stays idle. The windowed rate's exact
	// value depends on the real elapsed milliseconds between the back-to-back
	// calls, so assert the shape: exactly one row, alice, positive both ways.
	setNodeClientTotal(t, 1, "alice", 3000, 6000)
	setNodeClientTotal(t, 2, "alice", 1500, 1700)

	got := j.nodeClientSpeed()
	if len(got) != 1 {
		t.Fatalf("expected exactly one moving client, got %v", got)
	}
	if got[0].Email != "alice" || got[0].Up <= 0 || got[0].Down <= 0 {
		t.Fatalf("unexpected rate row: %+v", got[0])
	}

	// No movement on the next tick: alice's movement is still inside the
	// trailing window, so her (decaying) rate is still reported — that is
	// the smoothing working, not a stale pin.
	if got := j.nodeClientSpeed(); len(got) != 1 || got[0].Email != "alice" {
		t.Fatalf("windowed rate must persist across an idle tick, got %v", got)
	}
}

func TestNodeClientSpeed_CounterResetAdoptsLowerBaseline(t *testing.T) {
	initNodeClientSpeedDB(t)
	setNodeClientTotal(t, 1, "alice", 9000, 9000)

	j := &NodeTrafficSyncJob{inboundService: service.InboundService{}}
	_ = j.nodeClientSpeed() // baseline

	// Counter reset (e.g. traffic reset on the node): no spike, the series
	// rebases on the lower value.
	setNodeClientTotal(t, 1, "alice", 100, 100)
	if got := j.nodeClientSpeed(); len(got) != 0 {
		t.Fatalf("reset tick must produce no rates, got %v", got)
	}

	setNodeClientTotal(t, 1, "alice", 600, 1100)
	got := j.nodeClientSpeed()
	if len(got) != 1 || got[0].Email != "alice" || got[0].Up <= 0 || got[0].Down <= 0 {
		t.Fatalf("rate after reset must be measured from the new baseline, got %v", got)
	}
}

func TestGetNodeClientTrafficTotals_SumsAcrossNodes(t *testing.T) {
	initNodeClientSpeedDB(t)
	setNodeClientTotal(t, 1, "alice", 100, 200)
	setNodeClientTotal(t, 2, "alice", 30, 40)
	setNodeClientTotal(t, 2, "bob", 7, 8)

	svc := service.InboundService{}
	totals, err := svc.GetNodeClientTrafficTotals()
	if err != nil {
		t.Fatalf("GetNodeClientTrafficTotals: %v", err)
	}
	if totals["alice"] != [2]int64{130, 240} {
		t.Fatalf("alice totals = %v, want [130 240]", totals["alice"])
	}
	if totals["bob"] != [2]int64{7, 8} {
		t.Fatalf("bob totals = %v, want [7 8]", totals["bob"])
	}
}
