package ai

import "testing"

func TestPromptAssistantToChatMessageNormalizesBlankToolArguments(t *testing.T) {
	msg := PromptMessage{
		Role: PromptRoleAssistant,
		Blocks: []PromptBlock{{
			Type:              PromptBlockToolCall,
			ToolCallID:        "call_123",
			ToolName:          "search",
			ToolCallArguments: "   ",
		}},
	}

	assistant := promptAssistantToChatMessage(msg)
	if assistant == nil || len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].OfFunction == nil {
		t.Fatalf("expected one function tool call, got %#v", assistant)
	}
	if got := assistant.ToolCalls[0].OfFunction.Function.Arguments; got != "{}" {
		t.Fatalf("expected blank tool arguments to normalize to {}, got %q", got)
	}
}
