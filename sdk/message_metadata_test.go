package sdk

import "testing"

func TestCopyFromBaseDeepCopiesNestedJSON(t *testing.T) {
	src := &BaseMessageMetadata{
		CanonicalTurnData: map[string]any{
			"parts": []any{
				map[string]any{
					"type": "text",
					"text": "hello",
					"meta": map[string]any{"lang": "en"},
				},
			},
		},
		ToolCalls: []ToolCallMetadata{{
			CallID: "call-1",
			Input: map[string]any{
				"items": []any{
					map[string]any{"name": "before"},
				},
			},
			Output: map[string]any{
				"result": map[string]any{"value": "before"},
			},
		}},
	}

	var dst BaseMessageMetadata
	dst.CopyFromBase(src)

	src.CanonicalTurnData["parts"].([]any)[0].(map[string]any)["text"] = "changed"
	src.CanonicalTurnData["parts"].([]any)[0].(map[string]any)["meta"].(map[string]any)["lang"] = "fr"
	src.ToolCalls[0].Input["items"].([]any)[0].(map[string]any)["name"] = "after"
	src.ToolCalls[0].Output["result"].(map[string]any)["value"] = "after"

	part := dst.CanonicalTurnData["parts"].([]any)[0].(map[string]any)
	if got := part["text"]; got != "hello" {
		t.Fatalf("expected canonical text to remain deep-copied, got %v", got)
	}
	if got := part["meta"].(map[string]any)["lang"]; got != "en" {
		t.Fatalf("expected canonical nested map to remain deep-copied, got %v", got)
	}
	if got := dst.ToolCalls[0].Input["items"].([]any)[0].(map[string]any)["name"]; got != "before" {
		t.Fatalf("expected tool input to remain deep-copied, got %v", got)
	}
	if got := dst.ToolCalls[0].Output["result"].(map[string]any)["value"]; got != "before" {
		t.Fatalf("expected tool output to remain deep-copied, got %v", got)
	}
}

func TestBuildAssistantMetadataBundleUsesCanonicalTurnData(t *testing.T) {
	td := TurnData{
		ID:   "turn-1",
		Role: "assistant",
		Parts: []TurnPart{
			{Type: "text", Text: "hello world"},
			{Type: "reasoning", Reasoning: "think first"},
			{
				Type:       "tool",
				ToolCallID: "call-1",
				ToolName:   "web_search",
				ToolType:   "ai",
				Input:      map[string]any{"q": "hello"},
				Output:     map[string]any{"ok": true},
				State:      "output-available",
			},
			{Type: "file", URL: "mxc://file", MediaType: "image/png"},
		},
	}

	bundle := BuildAssistantMetadataBundle(AssistantMetadataBundleParams{
		TurnData:       td,
		ToolType:       "fallback",
		FinishReason:   "completed",
		AgentID:        "agent-1",
		StartedAtMs:    1,
		CompletedAtMs:  2,
		PromptTokens:   3,
		CompletionID:   "resp-1",
		Model:          "model-1",
		FirstTokenAtMs: 4,
	})

	if bundle.Base.Body != "hello world" {
		t.Fatalf("expected body from turn data, got %q", bundle.Base.Body)
	}
	if bundle.Base.ThinkingContent != "think first" {
		t.Fatalf("expected reasoning from turn data, got %q", bundle.Base.ThinkingContent)
	}
	if bundle.Base.TurnID != "turn-1" {
		t.Fatalf("expected turn id from turn data, got %q", bundle.Base.TurnID)
	}
	if len(bundle.Base.ToolCalls) != 1 || bundle.Base.ToolCalls[0].ToolType != "ai" {
		t.Fatalf("expected tool call metadata from turn data, got %#v", bundle.Base.ToolCalls)
	}
	if len(bundle.Base.GeneratedFiles) != 1 || bundle.Base.GeneratedFiles[0].URL != "mxc://file" {
		t.Fatalf("expected generated file metadata from turn data, got %#v", bundle.Base.GeneratedFiles)
	}
	if bundle.Assistant.CompletionID != "resp-1" || bundle.Assistant.Model != "model-1" {
		t.Fatalf("expected assistant metadata to remain populated, got %#v", bundle.Assistant)
	}
	if !bundle.Assistant.HasToolCalls {
		t.Fatalf("expected tool call flag to be derived from canonical turn data")
	}
	if bundle.Base.CanonicalTurnData["id"] != "turn-1" {
		t.Fatalf("expected canonical turn data to be preserved, got %#v", bundle.Base.CanonicalTurnData)
	}
}

func TestTurnToolCallsPrefersPartToolType(t *testing.T) {
	td := TurnData{
		Parts: []TurnPart{{
			Type:       "tool",
			ToolCallID: "call-1",
			ToolName:   "web_fetch",
			ToolType:   "native",
			State:      "output-available",
		}},
	}

	calls := TurnToolCalls(td, "fallback")
	if len(calls) != 1 {
		t.Fatalf("expected one tool call, got %#v", calls)
	}
	if calls[0].ToolType != "native" {
		t.Fatalf("expected part tool type to win, got %q", calls[0].ToolType)
	}
}
