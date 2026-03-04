package providers

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestBuildAnthropicParams_WithThinkingAndCacheControl(t *testing.T) {
	temp := 0.3
	params := BuildAnthropicParams(
		ai.Model{
			ID:      "claude-sonnet-4-5",
			BaseURL: "https://api.anthropic.com",
		},
		ai.Context{
			SystemPrompt: "You are helpful",
			Messages: []ai.Message{
				{Role: ai.RoleUser, Text: "hello"},
			},
			Tools: []ai.Tool{
				{Name: "calc", Description: "Calculate", Parameters: map[string]any{"type": "object"}},
			},
		},
		AnthropicOptions{
			StreamOptions: ai.StreamOptions{
				Temperature:    &temp,
				MaxTokens:      4096,
				CacheRetention: ai.CacheRetentionLong,
			},
			ThinkingEnabled:      true,
			ThinkingBudgetTokens: 2048,
			Effort:               "medium",
			InterleavedThinking:  true,
			ToolChoice:           "auto",
		},
	)

	if params["model"] != "claude-sonnet-4-5" {
		t.Fatalf("expected model id set")
	}
	if params["max_tokens"] != 4096 {
		t.Fatalf("expected max tokens 4096")
	}
	if params["temperature"] != 0.3 {
		t.Fatalf("expected temperature 0.3")
	}
	if params["anthropic-beta"] != "interleaved-thinking-2025-05-14" {
		t.Fatalf("expected interleaved thinking beta header")
	}
	systemBlocks, ok := params["system"].([]map[string]any)
	if !ok || len(systemBlocks) != 1 {
		t.Fatalf("expected one system block, got %#v", params["system"])
	}
	cacheControl, ok := systemBlocks[0]["cache_control"].(map[string]any)
	if !ok || cacheControl["ttl"] != "1h" {
		t.Fatalf("expected anthropic cache control ttl 1h, got %#v", systemBlocks[0]["cache_control"])
	}
	thinking, ok := params["thinking"].(map[string]any)
	if !ok || thinking["budget_tokens"] != 2048 {
		t.Fatalf("expected thinking budget tokens, got %#v", params["thinking"])
	}
	tools, ok := params["tools"].([]map[string]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected converted tools, got %#v", params["tools"])
	}
	toolChoice, ok := params["tool_choice"].(map[string]any)
	if !ok || toolChoice["type"] != "auto" {
		t.Fatalf("expected auto tool_choice, got %#v", params["tool_choice"])
	}
}

func TestConvertAnthropicMessages_ToolCallsAndToolResultFallback(t *testing.T) {
	messages := convertAnthropicMessages(
		ai.Model{ID: "claude-sonnet-4-5"},
		ai.Context{
			Messages: []ai.Message{
				{
					Role: ai.RoleAssistant,
					Content: []ai.ContentBlock{
						{
							Type:      ai.ContentTypeToolCall,
							ID:        "invalid:tool id!",
							Name:      "lookup",
							Arguments: map[string]any{"q": "go"},
						},
					},
				},
				{
					Role:       ai.RoleToolResult,
					ToolCallID: "invalid:tool id!",
					Content: []ai.ContentBlock{
						{Type: ai.ContentTypeImage, MimeType: "image/png", Data: "abc"},
					},
				},
			},
		},
	)

	if len(messages) != 2 {
		t.Fatalf("expected two converted messages, got %d", len(messages))
	}
	assistant := messages[0]
	content := assistant["content"].([]map[string]any)
	toolUse := content[0]
	if toolUse["type"] != "tool_use" {
		t.Fatalf("expected tool_use block, got %#v", toolUse)
	}
	if toolUse["id"] != "invalid_tool_id_" {
		t.Fatalf("expected sanitized tool call id, got %#v", toolUse["id"])
	}

	toolResult := messages[1]["content"].([]map[string]any)[0]
	innerContent := toolResult["content"].([]map[string]any)
	if innerContent[0]["text"] != "(see attached image)" {
		t.Fatalf("expected fallback text for non-text tool result, got %#v", innerContent[0]["text"])
	}
}
