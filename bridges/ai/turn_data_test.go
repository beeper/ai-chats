package ai

import (
	"testing"

	"github.com/beeper/agentremote/sdk"
)

func TestCanonicalPromptMessagesPrefersTurnData(t *testing.T) {
	meta := &MessageMetadata{}
	meta.CanonicalTurnSchema = sdk.CanonicalTurnDataSchemaV1
	meta.CanonicalTurnData = sdk.TurnData{
		ID:   "turn-1",
		Role: "assistant",
		Parts: []sdk.TurnPart{
			{Type: "text", Text: "hello"},
			{Type: "tool", ToolCallID: "call_1", ToolName: "search", Input: map[string]any{"query": "matrix"}, Output: map[string]any{"ok": true}},
		},
	}.ToMap()

	messages := canonicalPromptMessages(meta)
	if len(messages) != 2 {
		t.Fatalf("expected assistant + tool result, got %d messages", len(messages))
	}
	if messages[0].Role != PromptRoleAssistant {
		t.Fatalf("expected assistant role, got %q", messages[0].Role)
	}
	if messages[1].Role != PromptRoleToolResult {
		t.Fatalf("expected tool result role, got %q", messages[1].Role)
	}
}

func TestSetCanonicalPromptMessagesStoresTurnDataForUser(t *testing.T) {
	meta := &MessageMetadata{}
	setCanonicalPromptMessages(meta, []PromptMessage{{
		Role: PromptRoleUser,
		Blocks: []PromptBlock{{
			Type: PromptBlockText,
			Text: "hello",
		}},
	}})

	if meta.CanonicalTurnSchema != sdk.CanonicalTurnDataSchemaV1 {
		t.Fatalf("expected turn data schema, got %q", meta.CanonicalTurnSchema)
	}
	td, ok := canonicalTurnData(meta)
	if !ok {
		t.Fatalf("expected canonical turn data")
	}
	if td.Role != "user" || len(td.Parts) != 1 || td.Parts[0].Text != "hello" {
		t.Fatalf("unexpected turn data: %#v", td)
	}
}
