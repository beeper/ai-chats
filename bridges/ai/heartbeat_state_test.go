package ai

import "testing"

func TestManagedHeartbeatStateDueAtUsesLastHeartbeatFallback(t *testing.T) {
	state := managedHeartbeatState{
		IntervalMs:            60_000,
		LastHeartbeatSentAtMs: 1_000,
	}

	if got := state.dueAt(nil, 5_000); got != 61_000 {
		t.Fatalf("expected dueAt to use last heartbeat timestamp, got %d", got)
	}
}

func TestManagedHeartbeatStateDuplicateHeartbeatIsSessionAware(t *testing.T) {
	state := managedHeartbeatState{
		LastHeartbeatSessionKey: "!room-a:example.com",
		LastHeartbeatText:       "still alive",
		LastHeartbeatSentAtMs:   10_000,
	}

	if !state.isDuplicateHeartbeat("!room-a:example.com", "still alive", 20_000) {
		t.Fatal("expected same session/text within dedupe window to be treated as duplicate")
	}
	if state.isDuplicateHeartbeat("!room-b:example.com", "still alive", 20_000) {
		t.Fatal("expected different session to bypass duplicate check")
	}
	if state.isDuplicateHeartbeat("!room-a:example.com", "different", 20_000) {
		t.Fatal("expected different text to bypass duplicate check")
	}
	if state.isDuplicateHeartbeat("!room-a:example.com", "still alive", 10_000+heartbeatDedupeWindowMs+1) {
		t.Fatal("expected duplicate window expiry to bypass duplicate check")
	}
}
