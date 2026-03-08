package cron

import "testing"

func TestComputeNextRunAtMsEveryAlwaysReturnsFuture(t *testing.T) {
	schedule := Schedule{Kind: "every", EveryMs: 1000, AnchorMs: int64Ptr(1000)}
	next := ComputeNextRunAtMs(schedule, 2000)
	if next == nil {
		t.Fatal("expected next run")
	}
	if *next <= 2000 {
		t.Fatalf("expected future next run, got %d", *next)
	}
}

func TestValidateScheduleRejectsInvalidEvery(t *testing.T) {
	result := ValidateSchedule(Schedule{Kind: "every", EveryMs: 0})
	if result.Ok {
		t.Fatal("expected invalid every schedule")
	}
}

func TestValidateScheduleRejectsUnsupportedKind(t *testing.T) {
	result := ValidateSchedule(Schedule{Kind: "weird"})
	if result.Ok {
		t.Fatal("expected unsupported schedule kind to be rejected")
	}
}

func int64Ptr(v int64) *int64 {
	return &v
}
