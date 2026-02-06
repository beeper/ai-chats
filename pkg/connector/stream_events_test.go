package connector

import (
	"testing"

	"maunium.net/go/mautrix/id"
)

func TestBuildStreamEventEnvelope_RejectsEmptyTurnID(t *testing.T) {
	state := &streamingState{
		turnID:      "   ",
		sequenceNum: 7,
	}

	_, _, _, ok := buildStreamEventEnvelope(state, map[string]any{"type": "text-delta"})
	if ok {
		t.Fatalf("expected missing turn_id to be rejected")
	}
	if state.sequenceNum != 7 {
		t.Fatalf("sequence should not increment on rejected event, got %d", state.sequenceNum)
	}
}

func TestBuildStreamEventEnvelope_IncludesRequiredFields(t *testing.T) {
	state := &streamingState{
		turnID: "turn-1",
	}
	part := map[string]any{
		"type": "text-start",
		"id":   "text-1",
	}

	turnID, seq, content, ok := buildStreamEventEnvelope(state, part)
	if !ok {
		t.Fatalf("expected envelope to be built")
	}
	if turnID != "turn-1" {
		t.Fatalf("unexpected turn_id: %q", turnID)
	}
	if seq != 1 {
		t.Fatalf("unexpected seq: %d", seq)
	}
	if content["turn_id"] != "turn-1" {
		t.Fatalf("missing turn_id in content: %#v", content)
	}
	if content["seq"] != 1 {
		t.Fatalf("missing seq in content: %#v", content)
	}
	gotPart, ok := content["part"].(map[string]any)
	if !ok {
		t.Fatalf("expected part map, got %T", content["part"])
	}
	if gotPart["type"] != "text-start" || gotPart["id"] != "text-1" {
		t.Fatalf("unexpected part payload: %#v", gotPart)
	}
}

func TestBuildStreamEventEnvelope_TargetEventAndRelation(t *testing.T) {
	state := &streamingState{
		turnID:         "turn-1",
		initialEventID: id.EventID("$initial-event"),
	}

	_, _, content, ok := buildStreamEventEnvelope(state, map[string]any{"type": "text-delta"})
	if !ok {
		t.Fatalf("expected envelope to be built")
	}
	if got := content["target_event"]; got != "$initial-event" {
		t.Fatalf("unexpected target_event: %v", got)
	}
	relatesTo, ok := content["m.relates_to"].(map[string]any)
	if !ok {
		t.Fatalf("expected m.relates_to map, got %T", content["m.relates_to"])
	}
	if relatesTo["rel_type"] != RelReference {
		t.Fatalf("unexpected rel_type: %v", relatesTo["rel_type"])
	}
	if relatesTo["event_id"] != "$initial-event" {
		t.Fatalf("unexpected related event id: %v", relatesTo["event_id"])
	}
}

func TestBuildStreamEventEnvelope_SequenceIsMonotonic(t *testing.T) {
	state := &streamingState{
		turnID: "turn-1",
	}

	_, seq1, _, ok1 := buildStreamEventEnvelope(state, map[string]any{"type": "start"})
	_, seq2, _, ok2 := buildStreamEventEnvelope(state, map[string]any{"type": "text-delta"})
	if !ok1 || !ok2 {
		t.Fatalf("expected envelopes to be built")
	}
	if seq1 != 1 || seq2 != 2 {
		t.Fatalf("expected monotonic seq values 1,2 got %d,%d", seq1, seq2)
	}
}
