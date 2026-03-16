package agentremote

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
