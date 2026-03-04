package providers

import (
	"strings"
	"testing"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestNormalizeResponsesToolCallID(t *testing.T) {
	got := NormalizeResponsesToolCallID("call_abc|item+/==")
	if !strings.Contains(got, "|") {
		t.Fatalf("expected normalized id to keep pipe separator, got %q", got)
	}
	parts := strings.SplitN(got, "|", 2)
	if len(parts) != 2 {
		t.Fatalf("expected two parts in normalized id, got %q", got)
	}
	if strings.ContainsAny(parts[0], "+/=") {
		t.Fatalf("expected call id sanitized, got %q", parts[0])
	}
	if !strings.HasPrefix(parts[1], "fc") {
		t.Fatalf("expected item id to start with fc prefix, got %q", parts[1])
	}
}

func TestConvertResponsesMessages_NormalizesAllowedProviderToolIDs(t *testing.T) {
	model := ai.Model{
		ID:       "gpt-5",
		Provider: "openai",
		API:      ai.APIOpenAIResponses,
	}
	context := ai.Context{
		SystemPrompt: "system prompt",
		Messages: []ai.Message{
			{Role: ai.RoleUser, Text: "hi"},
			{
				Role: ai.RoleAssistant,
				Content: []ai.ContentBlock{
					{
						Type:      ai.ContentTypeToolCall,
						ID:        "call_abc|item+/==",
						Name:      "echo",
						Arguments: map[string]any{"message": "hello"},
					},
				},
				Provider:   "github-copilot",
				API:        ai.APIOpenAIResponses,
				Model:      "gpt-5.2-codex",
				StopReason: ai.StopReasonToolUse,
			},
			{
				Role:       ai.RoleToolResult,
				ToolCallID: "call_abc|item+/==",
				ToolName:   "echo",
				Content: []ai.ContentBlock{
					{Type: ai.ContentTypeText, Text: "hello"},
				},
			},
		},
	}

	output := ConvertResponsesMessages(model, context, openAIToolCallProviders, nil)
	if len(output) < 4 {
		t.Fatalf("expected converted response input items, got %d", len(output))
	}
	functionCall := output[2]
	callID, _ := functionCall["call_id"].(string)
	itemID, _ := functionCall["id"].(string)
	if callID == "call_abc" && strings.Contains(itemID, "+") {
		t.Fatalf("expected normalized function call ids, got call=%q item=%q", callID, itemID)
	}
	if !strings.HasPrefix(itemID, "fc") {
		t.Fatalf("expected function call item id to start with fc, got %q", itemID)
	}

	functionOutput := output[3]
	if functionOutput["call_id"] != callID {
		t.Fatalf("expected function_call_output call_id to match normalized call_id, got output=%q call=%q", functionOutput["call_id"], callID)
	}
}

func TestConvertResponsesMessages_CanOmitSystemPrompt(t *testing.T) {
	output := ConvertResponsesMessages(
		ai.Model{Provider: "openai", API: ai.APIOpenAIResponses},
		ai.Context{
			SystemPrompt: "system prompt",
			Messages: []ai.Message{
				{Role: ai.RoleUser, Text: "hello"},
			},
		},
		openAIToolCallProviders,
		&ConvertResponsesMessagesOptions{IncludeSystemPrompt: false},
	)
	if len(output) == 0 {
		t.Fatalf("expected user message output")
	}
	first := output[0]
	if role, _ := first["role"].(string); role == "system" || role == "developer" {
		t.Fatalf("expected no system/developer prompt in output when omitted, got %#v", first)
	}
}
