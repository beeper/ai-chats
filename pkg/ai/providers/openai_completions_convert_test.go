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
