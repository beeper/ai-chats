package ai

import (
	"testing"

	"github.com/beeper/agentremote/sdk"
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
