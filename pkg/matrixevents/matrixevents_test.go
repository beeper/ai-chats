package matrixevents

import "testing"

func TestBuildStreamEventEnvelope_RequiresTurnID(t *testing.T) {
	_, err := BuildStreamEventEnvelope("  ", 1, map[string]any{"type": "text-delta"}, StreamEventOpts{})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestBuildStreamEventEnvelope_RequiresSeq(t *testing.T) {
	_, err := BuildStreamEventEnvelope("turn1", 0, map[string]any{"type": "text-delta"}, StreamEventOpts{})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestBuildStreamEventEnvelope_RequiresRelatesToEventID(t *testing.T) {
	_, err := BuildStreamEventEnvelope("turn1", 1, map[string]any{"type": "text-delta"}, StreamEventOpts{})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestBuildStreamEventEnvelope_IncludesRelatesTo(t *testing.T) {
	content, err := BuildStreamEventEnvelope("turn1", 2, map[string]any{"type": "text-delta"}, StreamEventOpts{
		RelatesToEventID: "$event",
		AgentID:          "agent1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content["turn_id"] != "turn1" {
		t.Fatalf("turn_id mismatch")
	}
	if content["seq"] != 2 {
		t.Fatalf("seq mismatch")
	}
	if content["agent_id"] != "agent1" {
		t.Fatalf("agent_id mismatch")
	}
	rt, ok := content["m.relates_to"].(map[string]any)
	if !ok {
		t.Fatalf("missing m.relates_to")
	}
	if rt["rel_type"] != RelReference || rt["event_id"] != "$event" {
		t.Fatalf("unexpected m.relates_to: %#v", rt)
	}
	if _, ok := content["target_event"]; ok {
		t.Fatalf("did not expect target_event mirror: %#v", content)
	}
}

func TestBuildStreamEventTxnID(t *testing.T) {
	if got := BuildStreamEventTxnID("turn1", 5); got != "ai_stream_turn1_5" {
		t.Fatalf("unexpected txn id: %q", got)
	}
	if got := BuildStreamEventTxnID("", 5); got != "ai_stream_5" {
		t.Fatalf("unexpected txn id: %q", got)
	}
}
