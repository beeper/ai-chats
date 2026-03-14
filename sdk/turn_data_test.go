package sdk

import "testing"

func TestTurnDataFromUIMessageRoundTrip(t *testing.T) {
	ui := map[string]any{
		"id":   "turn-1",
		"role": "assistant",
		"metadata": map[string]any{
			"turn_id": "turn-1",
			"model":   "openai/gpt-5",
		},
		"parts": []any{
			map[string]any{"type": "text", "state": "done", "text": "hello"},
			map[string]any{
				"type":       "tool",
				"state":      "output-available",
				"toolCallId": "call_1",
				"toolName":   "search",
				"input":      map[string]any{"query": "matrix"},
				"output":     map[string]any{"result": "done"},
			},
		},
	}

	td, ok := TurnDataFromUIMessage(ui)
	if !ok {
		t.Fatalf("expected turn data")
	}
	if td.ID != "turn-1" || td.Role != "assistant" {
		t.Fatalf("unexpected identity: %#v", td)
	}
	if len(td.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(td.Parts))
	}

	roundTrip := UIMessageFromTurnData(td)
	if got := roundTrip["id"]; got != "turn-1" {
		t.Fatalf("unexpected round-trip id: %#v", got)
	}
	parts, ok := roundTrip["parts"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("expected 2 round-trip parts, got %#v", roundTrip["parts"])
	}
}
