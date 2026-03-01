package streamtransport

import (
	"testing"
	"time"
)

func TestEditDebounceGate(t *testing.T) {
	gate := NewEditDebounceGate()
	base := time.Unix(1, 0)

	if !gate.ShouldEmit("turn-1", "hello", base, 200*time.Millisecond) {
		t.Fatal("expected first body to emit")
	}
	if gate.ShouldEmit("turn-1", "hello", base.Add(1*time.Second), 200*time.Millisecond) {
		t.Fatal("expected identical body to be skipped")
	}
	if gate.ShouldEmit("turn-1", "hello 2", base.Add(100*time.Millisecond), 200*time.Millisecond) {
		t.Fatal("expected debounce to skip fast update")
	}
	if !gate.ShouldEmit("turn-1", "hello 2", base.Add(250*time.Millisecond), 200*time.Millisecond) {
		t.Fatal("expected changed body after debounce to emit")
	}
	gate.Clear("turn-1")
	if !gate.ShouldEmit("turn-1", "hello 2", base.Add(260*time.Millisecond), 200*time.Millisecond) {
		t.Fatal("expected cleared gate to emit")
	}
}

func TestEditDebounceGateStaleEviction(t *testing.T) {
	gate := NewEditDebounceGate()
	base := time.Unix(1, 0)

	// Seed two turns
	gate.ShouldEmit("old-turn", "body", base, 0)
	gate.ShouldEmit("new-turn", "body", base.Add(29*time.Minute), 0)

	// Advance past TTL for old-turn but not new-turn
	gate.ShouldEmit("trigger", "x", base.Add(staleTurnTTL+1*time.Second), 0)

	gate.mu.Lock()
	_, hasOld := gate.turns["old-turn"]
	_, hasNew := gate.turns["new-turn"]
	gate.mu.Unlock()

	if hasOld {
		t.Fatal("expected old-turn to be evicted")
	}
	if !hasNew {
		t.Fatal("expected new-turn to be retained")
	}
}

func TestSplitAtMarkdownBoundary(t *testing.T) {
	text := "a\n\nb\n\nc"
	first, rest := SplitAtMarkdownBoundary(text, 4)
	if first == "" || rest == "" {
		t.Fatalf("expected split result, got first=%q rest=%q", first, rest)
	}
}
