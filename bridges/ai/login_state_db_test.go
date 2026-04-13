package ai

import (
	"testing"
	"time"
)

func TestLoginRuntimeStateUpdateHeartbeat(t *testing.T) {
	state := &loginRuntimeState{}
	first := &HeartbeatEventPayload{
		TS:      1,
		Status:  "sent",
		Reason:  "ok",
		To:      "a",
		Channel: "sms",
		Preview: "hello",
	}
	if !state.UpdateHeartbeat(first) {
		t.Fatalf("expected first heartbeat to update state")
	}
	if state.LastHeartbeatEvent == first {
		t.Fatalf("expected heartbeat payload to be cloned")
	}
	if state.LastHeartbeatEvent == nil || state.LastHeartbeatEvent.Status != "sent" {
		t.Fatalf("unexpected heartbeat state: %#v", state.LastHeartbeatEvent)
	}

	duplicate := &HeartbeatEventPayload{
		TS:      1,
		Status:  "sent",
		Reason:  "ok",
		To:      "a",
		Channel: "sms",
		Preview: "hello",
	}
	if state.UpdateHeartbeat(duplicate) {
		t.Fatalf("expected duplicate heartbeat to be ignored")
	}

	next := &HeartbeatEventPayload{
		TS:      2,
		Status:  "failed",
		Reason:  "timeout",
		To:      "b",
		Channel: "email",
		Preview: "world",
	}
	if !state.UpdateHeartbeat(next) {
		t.Fatalf("expected changed heartbeat to update state")
	}
	if state.LastHeartbeatEvent == nil || state.LastHeartbeatEvent.Status != "failed" {
		t.Fatalf("unexpected updated heartbeat state: %#v", state.LastHeartbeatEvent)
	}
}

func TestLoginRuntimeStateProviderHealthTransitions(t *testing.T) {
	state := &loginRuntimeState{ConsecutiveErrors: healthWarningThreshold - 1}
	now := time.Unix(123, 0)

	nextErrors, crossed := state.RecordProviderError(now, healthWarningThreshold)
	if nextErrors != healthWarningThreshold {
		t.Fatalf("unexpected error count: got %d want %d", nextErrors, healthWarningThreshold)
	}
	if !crossed {
		t.Fatalf("expected threshold crossing to be reported")
	}
	if state.LastErrorAt != now.Unix() {
		t.Fatalf("unexpected last error timestamp: got %d want %d", state.LastErrorAt, now.Unix())
	}

	if !state.RecordProviderSuccess(healthWarningThreshold) {
		t.Fatalf("expected recovery after threshold breach")
	}
	if state.ConsecutiveErrors != 0 || state.LastErrorAt != 0 {
		t.Fatalf("expected provider success to clear error state: %#v", state)
	}

	state = &loginRuntimeState{}
	if state.RecordProviderSuccess(healthWarningThreshold) {
		t.Fatalf("expected empty error state to remain unchanged")
	}
}
