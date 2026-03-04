package providers

import (
	"testing"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func anthropicNormalizeID(id string, _ ai.Model, _ ai.Message) string {
	sanitized := ""
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			sanitized += string(r)
		} else {
			sanitized += "_"
		}
	}
	if len(sanitized) > 64 {
		return sanitized[:64]
	}
	return sanitized
}

func TestTransformMessages_OpenAIToAnthropicCopilot(t *testing.T) {
	model := ai.Model{
		ID:       "claude-sonnet-4",
		API:      ai.APIAnthropicMessages,
		Provider: "github-copilot",
	}
	now := time.Now().UnixMilli()

	messages := []ai.Message{
		{Role: ai.RoleUser, Text: "hello", Timestamp: now},
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeThinking, Thinking: "Let me think", ThinkingSignature: "reasoning_content"},
				{Type: ai.ContentTypeText, Text: "Hi there!"},
			},
			API:        ai.APIOpenAICompletions,
			Provider:   "github-copilot",
			Model:      "gpt-4o",
			StopReason: ai.StopReasonStop,
			Timestamp:  now,
		},
	}

	result := TransformMessages(messages, model, anthropicNormalizeID)
	var assistant ai.Message
	for _, msg := range result {
		if msg.Role == ai.RoleAssistant {
			assistant = msg
			break
		}
	}
	thinkingBlocks := 0
	textBlocks := 0
	for _, block := range assistant.Content {
		if block.Type == ai.ContentTypeThinking {
			thinkingBlocks++
		}
		if block.Type == ai.ContentTypeText {
			textBlocks++
		}
	}
	if thinkingBlocks != 0 {
		t.Fatalf("expected no thinking blocks after cross-model transform")
	}
	if textBlocks < 2 {
		t.Fatalf("expected at least two text blocks after transform, got %d", textBlocks)
	}
}

func TestTransformMessages_RemovesThoughtSignatureAcrossModels(t *testing.T) {
	model := ai.Model{
		ID:       "claude-sonnet-4",
		API:      ai.APIAnthropicMessages,
		Provider: "github-copilot",
	}
	now := time.Now().UnixMilli()
	messages := []ai.Message{
		{Role: ai.RoleUser, Text: "run command", Timestamp: now},
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{{
				Type:             ai.ContentTypeToolCall,
				ID:               "call_123",
				Name:             "bash",
				Arguments:        map[string]any{"command": "ls"},
				ThoughtSignature: `{"type":"reasoning.encrypted"}`,
			}},
			API:        ai.APIOpenAIResponses,
			Provider:   "github-copilot",
			Model:      "gpt-5",
			StopReason: ai.StopReasonToolUse,
			Timestamp:  now,
		},
		{
			Role:       ai.RoleToolResult,
			ToolCallID: "call_123",
			ToolName:   "bash",
			Content: []ai.ContentBlock{{
				Type: ai.ContentTypeText,
				Text: "output",
			}},
			Timestamp: now,
		},
	}

	result := TransformMessages(messages, model, anthropicNormalizeID)
	for _, msg := range result {
		if msg.Role != ai.RoleAssistant {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == ai.ContentTypeToolCall && block.ThoughtSignature != "" {
				t.Fatalf("expected thoughtSignature to be removed across model handoff")
			}
		}
	}
}

func TestTransformMessages_SynthesizesMissingToolResultBeforeNextUserTurn(t *testing.T) {
	model := ai.Model{
		ID:       "gpt-4o-mini",
		API:      ai.APIOpenAICompletions,
		Provider: "openai",
	}
	now := time.Now().UnixMilli()
	input := []ai.Message{
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{
				{
					Type:      ai.ContentTypeToolCall,
					ID:        "call_1",
					Name:      "calculate",
					Arguments: map[string]any{"expression": "25*18"},
				},
			},
			StopReason: ai.StopReasonToolUse,
			Timestamp:  now,
		},
		{
			Role:      ai.RoleUser,
			Text:      "Never mind, what is 2+2?",
			Timestamp: now + 1,
		},
	}

	result := TransformMessages(input, model, nil)
	if len(result) != 3 {
		t.Fatalf("expected synthesized tool result to be inserted, got %d messages", len(result))
	}
	if result[1].Role != ai.RoleToolResult {
		t.Fatalf("expected synthesized message at index 1 to be toolResult, got %s", result[1].Role)
	}
	if result[1].ToolCallID != "call_1" || result[1].ToolName != "calculate" || !result[1].IsError {
		t.Fatalf("unexpected synthesized toolResult payload: %#v", result[1])
	}
	if len(result[1].Content) == 0 || result[1].Content[0].Text != "No result provided" {
		t.Fatalf("expected synthesized toolResult content fallback")
	}
}
