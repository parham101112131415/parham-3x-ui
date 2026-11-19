package job

import "testing"

// A steady 700 KB/s flow sampled once a second must read 700 KB/s, not
// swing with each poll's chunking.
func TestSpeedTracker_SteadyFlowReadsSteady(t *testing.T) {
	tr := newSpeedTracker()
	const rate = 700_000
	var cum int64
	if _, _, ok := tr.rate("a", 0, 0, 0); ok {
		t.Fatal("first sample must only seed the series")
	}
	for s := int64(1); s <= 5; s++ {
		cum += rate
		_, down, ok := tr.rate("a", s*1000, 0, cum)
		if !ok {
			t.Fatalf("t=%ds: expected a quotable rate", s)
		}
		if down != rate {
			t.Fatalf("t=%ds: down rate = %d, want %d", s, down, rate)
		}
	}
}

// The same flow arriving in one 3s chunk (a slow node poll) must read the
// same rate once the chunk lands — the old per-poll math showed ~3x during
// the chunk poll and ~0 in the polls before it.
func TestSpeedTracker_ChunkyArrivalIsAveraged(t *testing.T) {
	tr := newSpeedTracker()
	_, _, _ = tr.rate("a", 0, 0, 0)
	_, _, _ = tr.rate("a", 1000, 0, 0)
	_, _, _ = tr.rate("a", 2000, 0, 0)
	_, down, ok := tr.rate("a", 3000, 0, 2_100_000)
	if !ok || down != 700_000 {
		t.Fatalf("chunk landing: down = %d ok = %v, want 700000 true", down, ok)
	}
	// One more steady second: still 700 KB/s over the 4s span.
	_, down, ok = tr.rate("a", 4000, 0, 2_800_000)
	if !ok || down != 700_000 {
		t.Fatalf("after chunk: down = %d ok = %v, want 700000 true", down, ok)
	}
}

// A counter reset rebases the series: no negative spike, and the next rate
// is measured from the new baseline only.
func TestSpeedTracker_CounterResetRebases(t *testing.T) {
	tr := newSpeedTracker()
	_, _, _ = tr.rate("a", 0, 9000, 9000)
	_, _, _ = tr.rate("a", 1000, 9500, 9500)
	if _, _, ok := tr.rate("a", 2000, 100, 100); ok {
		t.Fatal("reset sample must not produce a rate")
	}
	up, down, ok := tr.rate("a", 3000, 600, 1100)
	if !ok || up != 500 || down != 1000 {
		t.Fatalf("post-reset rate = %d/%d ok = %v, want 500/1000 true", up, down, ok)
	}
}

// Once movement leaves the trailing window the key goes quiet instead of
// pinning its last rate forever.
func TestSpeedTracker_IdleKeyGoesQuiet(t *testing.T) {
	tr := newSpeedTracker()
	_, _, _ = tr.rate("a", 0, 0, 0)
	if _, _, ok := tr.rate("a", 1000, 0, 5000); !ok {
		t.Fatal("moving key must produce a rate")
	}
	for s := int64(2); s <= 7; s++ {
		_, _, ok := tr.rate("a", s*1000, 0, 5000)
		if s == 7 && ok {
			t.Fatal("key idle past the window must go quiet")
		}
	}
}

func TestSpeedTracker_ForgetExcept(t *testing.T) {
	tr := newSpeedTracker()
	_, _, _ = tr.rate("a", 0, 0, 0)
	_, _, _ = tr.rate("b", 0, 0, 0)
	tr.forgetExcept(map[string]bool{"a": true})
	if _, _, ok := tr.rate("b", 1000, 0, 5000); ok {
		t.Fatal("forgotten key must restart from a baseline")
	}
}
