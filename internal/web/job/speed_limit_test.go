package job

import "testing"

func TestBucketStepAtRateNeverSuspends(t *testing.T) {
	limit := int64(1_000_000)
	b := &speedBucket{tokens: float64(limit) * speedBurstSeconds}
	for i := 0; i < 120; i++ {
		if got := bucketStep(b, limit, 1, limit); got != 0 {
			t.Fatalf("tick %d: at-rate usage must never trigger an action, got %d", i, got)
		}
	}
}

func TestBucketStepDoubleRateSuspendsAfterBurst(t *testing.T) {
	limit := int64(1_000_000)
	b := &speedBucket{tokens: float64(limit) * speedBurstSeconds}
	// At 2x rate the 5s burst drains after ~5 ticks of net -1s budget each.
	suspendedAt := -1
	for i := 0; i < 20; i++ {
		if got := bucketStep(b, limit, 1, 2*limit); got == 1 {
			suspendedAt = i
			break
		}
	}
	if suspendedAt < 0 {
		t.Fatal("sustained 2x rate never suspended")
	}
	if suspendedAt > 8 {
		t.Fatalf("suspension too late: tick %d", suspendedAt)
	}
}

func TestBucketStepSuspendedRepaysAndResumes(t *testing.T) {
	limit := int64(1_000_000)
	b := &speedBucket{tokens: -2_000_000, suspended: true}
	// No usage while suspended: +1s of budget per tick; resume needs tokens
	// back to +1s of rate, i.e. 3 ticks from -2s.
	steps := []int{}
	for i := 0; i < 6; i++ {
		steps = append(steps, bucketStep(b, limit, 1, 0))
	}
	if steps[0] != 0 || steps[1] != 0 || steps[2] != -1 {
		t.Fatalf("want resume on the 3rd idle tick, got %v", steps)
	}
}
