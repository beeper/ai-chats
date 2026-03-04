package connector

import "testing"

func TestToAIContext_MapsMessagesAndTools(t *testing.T) {
	inputMessages := []UnifiedMessage{
		{
			Role: RoleUser,
			Content: []ContentPart{
				{Type: ContentTypeText, Text: "hello"},
			},
		},
		{
			Role: RoleAssistant,
			Content: []ContentPart{
				{Type: ContentTypeText, Text: "use tool"},
			},
			ToolCalls: []ToolCallResult{{
				ID:        "call_1",
				Name:      "echo",
				Arguments: `{"message":"hi"}`,
			}},
		},
		{
			Role:       RoleTool,
			ToolCallID: "call_1",
			Name:       "echo",
			Content: []ContentPart{
				{Type: ContentTypeText, Text: "hi"},
			},
		},
	}
	tools := []ToolDefinition{{
		Name:        "echo",
		Description: "Echo message",
		Parameters: map[string]any{
			"type": "object",
		},
	}}

	ctx := toAIContext("system prompt", inputMessages, tools)
	if ctx.SystemPrompt != "system prompt" {
		t.Fatalf("unexpected system prompt: %s", ctx.SystemPrompt)
	}
	if len(ctx.Messages) != 3 {
		t.Fatalf("expected 3 mapped messages, got %d", len(ctx.Messages))
	}
	if ctx.Messages[0].Role != "user" {
		t.Fatalf("expected first role user, got %s", ctx.Messages[0].Role)
	}
	if ctx.Messages[1].Role != "assistant" {
		t.Fatalf("expected second role assistant, got %s", ctx.Messages[1].Role)
	}
	if len(ctx.Messages[1].Content) < 2 {
		t.Fatalf("expected assistant content to include text and tool call, got %+v", ctx.Messages[1].Content)
	}
	toolCall := ctx.Messages[1].Content[1]
	if toolCall.Type != "toolCall" || toolCall.Name != "echo" {
		t.Fatalf("unexpected tool call mapping: %+v", toolCall)
	}
	if toolCall.Arguments["message"] != "hi" {
		t.Fatalf("unexpected parsed tool args: %+v", toolCall.Arguments)
	}
	if ctx.Messages[2].Role != "toolResult" {
		t.Fatalf("expected tool role mapped to toolResult, got %s", ctx.Messages[2].Role)
	}
	if len(ctx.Tools) != 1 || ctx.Tools[0].Name != "echo" {
		t.Fatalf("expected mapped tools in context, got %+v", ctx.Tools)
	}
}
