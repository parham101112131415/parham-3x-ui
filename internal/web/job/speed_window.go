package job

import "sync"

// speedWindowMs is the trailing window a live speed reading is averaged
// over. Readings still refresh every poll (1s); the window only decides how
// much history each reading averages. The byte counters behind a reading
// arrive in uneven chunks — Xray polls, node snapshot fetches and database
// merges all run on independent ~1s cadences, so one poll can carry three
// seconds of bytes and the next almost none. Dividing a single poll's chunk
// by a fixed 1s (the old behaviour) swung a steady 700 KB/s flow between
// ~5 KB/s and ~2 MB/s on the dashboard; averaging the cumulative counters
// over a few seconds tracks the real rate.
const speedWindowMs int64 = 5000

// speedPoint is one observation of a cumulative byte counter.
type speedPoint struct {
	at       int64 // unix millis
	up, down int64 // cumulative bytes
}

// speedTracker derives stable per-key rates (bytes/sec) from cumulative
// counters sampled at irregular times. A counter that moves backwards (a
// traffic reset, a core restart) rebases that key's series instead of
// producing a negative or absurd rate.
type speedTracker struct {
	mu     sync.Mutex
	series map[string][]speedPoint
}

func newSpeedTracker() *speedTracker {
	return &speedTracker{series: make(map[string][]speedPoint)}
}

// rate records the key's cumulative counters at time at (unix millis) and
// returns the average up/down rate in bytes/sec over the trailing window.
// ok is false when there is not yet a quotable rate: first sighting, a
// just-rebased series, or no movement inside the window.
func (t *speedTracker) rate(key string, at, cumUp, cumDown int64) (up, down int64, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	pts := t.series[key]
	appendNew := true
	if n := len(pts); n > 0 {
		last := pts[n-1]
		switch {
		case cumUp < last.up || cumDown < last.down:
			pts = pts[:0] // counter reset: rebase on the new value
		case at < last.at:
			return 0, 0, false // stale/out-of-order sample; keep the series
		case at == last.at:
			// Two samples inside one millisecond: the newer reading
			// replaces the previous point instead of being dropped.
			pts[n-1] = speedPoint{at: at, up: cumUp, down: cumDown}
			appendNew = false
		}
	}
	if appendNew {
		pts = append(pts, speedPoint{at: at, up: cumUp, down: cumDown})
	}
	// Prune to the window, keeping the newest point at or before the edge
	// as the base so the rate spans the full window.
	cutoff := at - speedWindowMs
	base := 0
	for i, p := range pts {
		if p.at > cutoff {
			break
		}
		base = i
	}
	if base > 0 {
		pts = pts[base:]
	}
	t.series[key] = pts
	if len(pts) < 2 {
		return 0, 0, false
	}
	first, last := pts[0], pts[len(pts)-1]
	span := last.at - first.at
	if span <= 0 {
		return 0, 0, false
	}
	dUp := last.up - first.up
	dDown := last.down - first.down
	if dUp <= 0 && dDown <= 0 {
		return 0, 0, false
	}
	if dUp < 0 {
		dUp = 0
	}
	if dDown < 0 {
		dDown = 0
	}
	return dUp * 1000 / span, dDown * 1000 / span, true
}

// forgetExcept drops the series of every key not in keep, so removed
// clients and inbounds do not linger in memory.
func (t *speedTracker) forgetExcept(keep map[string]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.series {
		if !keep[k] {
			delete(t.series, k)
		}
	}
}
