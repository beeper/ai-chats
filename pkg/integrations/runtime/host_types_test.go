package runtime

import (
	"testing"
	"time"
)

func TestMemoryStateApplyCompactionLifecycle(t *testing.T) {
	now := time.UnixMilli(12345)
	state := &MemoryState{}

	state.ApplyCompactionLifecycle(CompactionLifecycleStart, 0, "", now)
	if !state.CompactionInFlight {
		t.Fatalf("expected compaction to be marked in flight")
	}

	state.ApplyCompactionLifecycle(CompactionLifecycleEnd, 7, "", now)
	if state.CompactionInFlight {
		t.Fatalf("expected compaction to be cleared after end")
	}
	if state.LastCompactionAt != now.UnixMilli() {
		t.Fatalf("unexpected last compaction at: got %d want %d", state.LastCompactionAt, now.UnixMilli())
	}
	if state.LastCompactionDroppedCount != 7 {
		t.Fatalf("unexpected dropped count: got %d want %d", state.LastCompactionDroppedCount, 7)
	}

	state.ApplyCompactionLifecycle(CompactionLifecycleFail, 0, " boom \n", now)
	if state.CompactionInFlight {
		t.Fatalf("expected compaction to be cleared after failure")
	}
	if state.LastCompactionError != "boom" {
		t.Fatalf("unexpected compaction error: %q", state.LastCompactionError)
	}

	state.ApplyCompactionLifecycle(CompactionLifecycleRefresh, 0, "", now)
	if state.LastCompactionRefreshAt != now.UnixMilli() {
		t.Fatalf("unexpected refresh timestamp: got %d want %d", state.LastCompactionRefreshAt, now.UnixMilli())
	}
}

func TestMemoryStateOverflowAndBootstrapHelpers(t *testing.T) {
	now := time.UnixMilli(23456)
	state := &MemoryState{}

	if state.AlreadyFlushed(4) {
		t.Fatalf("expected empty state to report not flushed")
	}
	state.MarkOverflowFlushed(4, now)
	if state.OverflowFlushAt != now.UnixMilli() {
		t.Fatalf("unexpected overflow flush timestamp: got %d want %d", state.OverflowFlushAt, now.UnixMilli())
	}
	if !state.AlreadyFlushed(4) {
		t.Fatalf("expected matching compaction counter to report flushed")
	}
	if state.AlreadyFlushed(5) {
		t.Fatalf("expected different compaction counter to report not flushed")
	}

	if !(&MemoryState{}).NeedsBootstrap() {
		t.Fatalf("expected zero bootstrap state to need bootstrap")
	}
	state.MarkBootstrapped(now)
	if state.NeedsBootstrap() {
		t.Fatalf("expected bootstrapped state to stop needing bootstrap")
	}
	if state.MemoryBootstrapAt != now.UnixMilli() {
		t.Fatalf("unexpected bootstrap timestamp: got %d want %d", state.MemoryBootstrapAt, now.UnixMilli())
	}
}

func TestNilMemoryStateHelpers(t *testing.T) {
	var state *MemoryState
	now := time.UnixMilli(34567)

	state.ApplyCompactionLifecycle(CompactionLifecycleEnd, 3, "boom", now)
	state.MarkOverflowFlushed(2, now)
	state.MarkBootstrapped(now)

	if state.AlreadyFlushed(2) {
		t.Fatalf("nil state should never report flushed")
	}
	if !state.NeedsBootstrap() {
		t.Fatalf("nil state should require bootstrap")
	}
}
