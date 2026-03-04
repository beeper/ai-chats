package providers

import (
	"testing"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestConvertOpenAICompletionsMessages_BatchesToolResultImages(t *testing.T) {
	model := ai.Model{
		ID:       "gpt-4o-mini",
		API:      ai.APIOpenAICompletions,
		Provider: "openai",
		BaseURL:  "https://api.openai.com/v1",
		Input:    []string{"text", "image"},
	}
	now := time.Now().UnixMilli()
	context := ai.Context{
		Messages: []ai.Message{
			{Role: ai.RoleUser, Text: "Read the images", Timestamp: now - 2},
			{
				Role: ai.RoleAssistant,
				Content: []ai.ContentBlock{
					{Type: ai.ContentTypeToolCall, ID: "tool-1", Name: "read", Arguments: map[string]any{"path": "img-1.png"}},
					{Type: ai.ContentTypeToolCall, ID: "tool-2", Name: "read", Arguments: map[string]any{"path": "img-2.png"}},
				},
				Provider:   "openai",
				API:        ai.APIOpenAICompletions,
				Model:      "gpt-4o-mini",
				StopReason: ai.StopReasonToolUse,
				Timestamp:  now,
			},
			{
				Role:       ai.RoleToolResult,
				ToolCallID: "tool-1",
				ToolName:   "read",
				Content: []ai.ContentBlock{
					{Type: ai.ContentTypeText, Text: "Read image file [image/png]"},
					{Type: ai.ContentTypeImage, Data: "ZmFrZQ==", MimeType: "image/png"},
				},
				Timestamp: now + 1,
			},
			{
				Role:       ai.RoleToolResult,
				ToolCallID: "tool-2",
				ToolName:   "read",
				Content: []ai.ContentBlock{
					{Type: ai.ContentTypeText, Text: "Read image file [image/png]"},
					{Type: ai.ContentTypeImage, Data: "ZmFrZQ==", MimeType: "image/png"},
				},
				Timestamp: now + 2,
			},
		},
	}

	compat := GetCompat(model)
	messages := ConvertOpenAICompletionsMessages(model, context, compat)
	if len(messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(messages))
	}
	roles := []string{
		messages[0].Role,
		messages[1].Role,
		messages[2].Role,
		messages[3].Role,
		messages[4].Role,
	}
	expected := []string{"user", "assistant", "tool", "tool", "user"}
	for i := range expected {
		if roles[i] != expected[i] {
			t.Fatalf("unexpected roles: %+v", roles)
		}
	}
	content, ok := messages[4].Content.([]map[string]any)
	if !ok {
		t.Fatalf("expected final user content array, got %T", messages[4].Content)
	}
	imageCount := 0
	for _, part := range content {
		if part["type"] == "image_url" {
			imageCount++
		}
	}
	if imageCount != 2 {
		t.Fatalf("expected 2 image parts, got %d", imageCount)
	}
}

func TestConvertOpenAICompletionsMessages_NormalizesPipeSeparatedToolCallIDs(t *testing.T) {
	model := ai.Model{
		ID:       "gpt-4o-mini",
		API:      ai.APIOpenAICompletions,
		Provider: "openrouter",
		BaseURL:  "https://openrouter.ai/api/v1",
		Input:    []string{"text"},
	}
	now := time.Now().UnixMilli()
	context := ai.Context{
		Messages: []ai.Message{
			{Role: ai.RoleUser, Text: "Use tool", Timestamp: now},
			{
				Role: ai.RoleAssistant,
				Content: []ai.ContentBlock{
					{
						Type:      ai.ContentTypeToolCall,
						ID:        "call_abc123|this-is-a-very-long-item-id-with-specials+/==",
						Name:      "echo",
						Arguments: map[string]any{"message": "hello"},
					},
				},
				Provider:   "github-copilot",
				API:        ai.APIOpenAIResponses,
				Model:      "gpt-5.2-codex",
				StopReason: ai.StopReasonToolUse,
				Timestamp:  now + 1,
			},
			{
				Role:       ai.RoleToolResult,
				ToolCallID: "call_abc123|this-is-a-very-long-item-id-with-specials+/==",
				ToolName:   "echo",
				Content: []ai.ContentBlock{
					{Type: ai.ContentTypeText, Text: "hello"},
				},
				Timestamp: now + 2,
			},
		},
	}

	messages := ConvertOpenAICompletionsMessages(model, context, GetCompat(model))
	if len(messages) < 3 {
		t.Fatalf("expected converted messages, got %d", len(messages))
	}
	assistant := messages[1]
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected single assistant tool call, got %#v", assistant.ToolCalls)
	}
	normalizedID, _ := assistant.ToolCalls[0]["id"].(string)
	if normalizedID == "" || normalizedID == "call_abc123|this-is-a-very-long-item-id-with-specials+/==" {
		t.Fatalf("expected tool call id to be normalized, got %q", normalizedID)
	}

	toolResult := messages[2]
	if toolResult.Role != "tool" {
		t.Fatalf("expected tool message, got %s", toolResult.Role)
	}
	if toolResult.ToolCallID != normalizedID {
		t.Fatalf("expected tool result id to match normalized call id, got tool=%q assistant=%q", toolResult.ToolCallID, normalizedID)
	}
}
