package providers

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestBuildOpenAIResponsesParams_ReasoningAndGPT5Fallback(t *testing.T) {
	model := ai.Model{
		ID:        "gpt-5",
		Name:      "GPT-5",
		Provider:  "openai",
		API:       ai.APIOpenAIResponses,
		Reasoning: true,
		BaseURL:   "https://api.openai.com/v1",
	}

	withReasoning := BuildOpenAIResponsesParams(model, ai.Context{
		Messages: []ai.Message{{Role: ai.RoleUser, Text: "hello"}},
	}, OpenAIResponsesOptions{
		ReasoningEffort:  ai.ThinkingHigh,
		ReasoningSummary: "detailed",
	})
	reasoning, ok := withReasoning["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" || reasoning["summary"] != "detailed" {
		t.Fatalf("expected explicit reasoning payload, got %#v", withReasoning["reasoning"])
	}
	include, ok := withReasoning["include"].([]string)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("expected include reasoning encrypted content, got %#v", withReasoning["include"])
	}

	noReasoning := BuildOpenAIResponsesParams(model, ai.Context{
		Messages: []ai.Message{{Role: ai.RoleUser, Text: "hello"}},
	}, OpenAIResponsesOptions{})
	input := noReasoning["input"].([]map[string]any)
	last := input[len(input)-1]
	if last["role"] != "developer" {
		t.Fatalf("expected gpt-5 fallback developer hint when reasoning omitted, got %#v", last)
	}
}

func TestConvertOpenAIResponsesMessages_ToolCallIDPipeParsing(t *testing.T) {
	messages := ConvertOpenAIResponsesMessages(ai.Model{}, ai.Context{
		Messages: []ai.Message{
			{
				Role: ai.RoleAssistant,
				Content: []ai.ContentBlock{
					{
						Type:      ai.ContentTypeToolCall,
						ID:        "call_123|item_456",
						Name:      "lookup",
						Arguments: map[string]any{"q": "go"},
					},
				},
			},
			{
				Role:       ai.RoleToolResult,
				ToolCallID: "call_123|item_456",
				Content: []ai.ContentBlock{
					{Type: ai.ContentTypeText, Text: "result"},
				},
			},
		},
	})

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	call := messages[0]
	if call["call_id"] != "call_123" || call["id"] != "item_456" {
		t.Fatalf("expected split call_id/item_id, got %#v", call)
	}
	result := messages[1]
	if result["call_id"] != "call_123" {
		t.Fatalf("expected tool result call_id to strip item_id suffix, got %#v", result["call_id"])
	}
}
