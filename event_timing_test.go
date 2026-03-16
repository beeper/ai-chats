package agentremote

import (
	"testing"
	"time"
)

func TestResolveEventTimingPreservesTimestampAndComputesStreamOrder(t *testing.T) {
	ts := time.UnixMilli(1234)
	timing := ResolveEventTiming(ts, 0)
	if !timing.Timestamp.Equal(ts) {
		t.Fatalf("expected timestamp %v, got %v", ts, timing.Timestamp)
	}
	if timing.StreamOrder != ts.UnixMilli()*1000 {
		t.Fatalf("expected stream order %d, got %d", ts.UnixMilli()*1000, timing.StreamOrder)
	}
}

func TestNextEventTimingBumpsPastLastStreamOrder(t *testing.T) {
	ts := time.UnixMilli(1234)
	timing := NextEventTiming(1234001, ts)
	if !timing.Timestamp.Equal(ts) {
		t.Fatalf("expected timestamp %v, got %v", ts, timing.Timestamp)
	}
	if timing.StreamOrder != 1234002 {
		t.Fatalf("expected stream order 1234002, got %d", timing.StreamOrder)
	}
}
