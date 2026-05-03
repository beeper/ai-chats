package ai

import (
	"testing"

	"github.com/beeper/ai-chats/sdk"
)

func TestPromptMessagesFromTurnDataJSONEncodesPlainStringToolArguments(t *testing.T) {
	messages := promptMessagesFromTurnData(testPromptAssistantToolTurnData("hello"))
	if len(messages) == 0 || len(messages[0].Blocks) == 0 {
		t.Fatalf("expected assistant prompt message")
	}
	if got := messages[0].Blocks[0].ToolCallArguments; got != `"hello"` {
		t.Fatalf("expected plain string to be JSON-encoded, got %q", got)
	}
}

func TestPromptMessagesFromTurnDataPreservesJSONStringToolArguments(t *testing.T) {
	messages := promptMessagesFromTurnData(testPromptAssistantToolTurnData(`{"query":"matrix"}`))
	if len(messages) == 0 || len(messages[0].Blocks) == 0 {
		t.Fatalf("expected assistant prompt message")
	}
	if got := messages[0].Blocks[0].ToolCallArguments; got != `{"query":"matrix"}` {
		t.Fatalf("expected JSON string to stay canonical JSON, got %q", got)
	}
}

func TestBuildUserPromptTurnKeepsPromptBlocksAndTurnDataInSync(t *testing.T) {
	msg, td, ok := buildUserPromptTurn([]PromptBlock{
		{Type: PromptBlockText, Text: "hello"},
		{Type: PromptBlockImage, ImageB64: "aGVsbG8=", MimeType: "image/png"},
		{Type: PromptBlockText, Text: "   "},
	})
	if !ok {
		t.Fatal("expected canonical user prompt turn")
	}
	if msg.Role != PromptRoleUser {
		t.Fatalf("expected user role, got %#v", msg.Role)
	}
	if len(msg.Blocks) != 2 {
		t.Fatalf("expected filtered user prompt blocks, got %#v", msg.Blocks)
	}
	if got := sdk.TurnText(td); got != "hello" {
		t.Fatalf("expected canonical user text hello, got %q", got)
	}
	if len(td.Parts) != 2 || td.Parts[1].Type != "image" {
		t.Fatalf("expected synced turn parts, got %#v", td.Parts)
	}
}

func testPromptAssistantToolTurnData(input any) sdk.TurnData {
	return sdk.TurnData{
		Role: "assistant",
		Parts: []sdk.TurnPart{{
			Type:       "tool",
			ToolCallID: "call-1",
			ToolName:   "search",
			Input:      input,
		}},
	}
}
