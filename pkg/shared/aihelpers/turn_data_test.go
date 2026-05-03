package aihelpers

import (
	"testing"
)

func TestTurnDataFromUIMessageRoundTrip(t *testing.T) {
	ui := map[string]any{
		"id":   "turn-1",
		"role": "assistant",
		"metadata": map[string]any{
			"turn_id": "turn-1",
			"model":   "openai/gpt-5",
		},
		"bridgeHint": "keep-me",
		"parts": []any{
			map[string]any{"type": "text", "state": "done", "text": "hello"},
			map[string]any{
				"type":       "tool",
				"state":      "output-available",
				"toolCallId": "call_1",
				"toolName":   "search",
				"input":      map[string]any{"query": "matrix"},
				"output":     map[string]any{"result": "done"},
				"providerMetadata": map[string]any{
					"site_name": "Example",
				},
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
	if td.Extra["bridgeHint"] != "keep-me" {
		t.Fatalf("expected top-level extra to round-trip, got %#v", td.Extra)
	}
	if td.Parts[1].Extra["providerMetadata"] == nil {
		t.Fatalf("expected part extra to preserve providerMetadata, got %#v", td.Parts[1].Extra)
	}

	roundTrip := UIMessageFromTurnData(td)
	if got := roundTrip["id"]; got != "turn-1" {
		t.Fatalf("unexpected round-trip id: %#v", got)
	}
	if got := roundTrip["bridgeHint"]; got != "keep-me" {
		t.Fatalf("unexpected round-trip extra: %#v", got)
	}
	parts, ok := roundTrip["parts"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("expected 2 round-trip parts, got %#v", roundTrip["parts"])
	}
	toolPart, _ := parts[1].(map[string]any)
	if toolPart["providerMetadata"] == nil {
		t.Fatalf("expected part extra to survive round-trip, got %#v", toolPart)
	}
}

func TestBuildTurnDataFromUIMessageMergesRuntimeState(t *testing.T) {
	ui := map[string]any{
		"id":   "turn-1",
		"role": "assistant",
		"parts": []any{
			map[string]any{"type": "text", "text": "hello"},
		},
	}

	td := BuildTurnDataFromUIMessage(ui, TurnDataBuildOptions{
		Metadata:  map[string]any{"finish_reason": "stop"},
		Reasoning: "thinking",
		ToolCalls: []ToolCallMetadata{{
			CallID:   "tool-1",
			ToolName: "search",
			ToolType: "function",
			Status:   "output-available",
			Output:   map[string]any{"ok": true},
		}},
		GeneratedFiles: []GeneratedFileRef{{
			URL:      "mxc://file",
			MimeType: "image/png",
		}},
		ArtifactParts: []map[string]any{
			{"type": "source-url", "url": "https://example.com", "title": "Example"},
		},
	})

	if td.Metadata["finish_reason"] != "stop" {
		t.Fatalf("expected metadata merge, got %#v", td.Metadata)
	}
	if !TurnDataHasPartType(td, "reasoning") {
		t.Fatalf("expected reasoning part, got %#v", td.Parts)
	}
	if !TurnDataHasToolCall(td, "tool-1") {
		t.Fatalf("expected tool part, got %#v", td.Parts)
	}
	if !TurnDataHasURLPart(td, "file", "mxc://file") {
		t.Fatalf("expected generated file part, got %#v", td.Parts)
	}
	if !TurnDataHasURLPart(td, "source-url", "https://example.com") {
		t.Fatalf("expected source-url part, got %#v", td.Parts)
	}
}
