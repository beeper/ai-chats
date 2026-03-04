package e2e

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/ai/providers"
)

func TestOpenAIReasoningReplayE2E_SkipsAbortedReasoningHistory(t *testing.T) {
	requirePIAIE2E(t)
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY is not set")
	}
	model := openAIReasoningSourceModel()
	providers.ResetAPIProviders()

	context := ai.Context{
		SystemPrompt: "You are a helpful assistant.",
		Tools:        []ai.Tool{doubleNumberTool()},
		Messages: []ai.Message{
			{
				Role:      ai.RoleUser,
				Text:      "Use the double_number tool to double 21.",
				Timestamp: time.Now().UnixMilli(),
			},
			{
				Role: ai.RoleAssistant,
				Content: []ai.ContentBlock{
					{
						Type:              ai.ContentTypeThinking,
						Thinking:          "",
						ThinkingSignature: `{"type":"reasoning","id":"rs_123","summary":[{"type":"summary_text","text":"tool required"}]}`,
					},
				},
				Provider:   "openai",
				API:        ai.APIOpenAIResponses,
				Model:      model.ID,
				StopReason: ai.StopReasonAborted,
				Timestamp:  time.Now().UnixMilli(),
			},
			{
				Role:      ai.RoleUser,
				Text:      "Say hello to confirm you can continue.",
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}

	response, err := ai.CompleteSimple(model, context, &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			APIKey:    apiKey,
			MaxTokens: 256,
		},
		Reasoning: ai.ThinkingHigh,
	})
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if response.StopReason == ai.StopReasonError {
		t.Fatalf("expected no provider error, got %q", response.ErrorMessage)
	}
	if len(response.Content) == 0 {
		t.Fatalf("expected non-empty response content")
	}
}

func TestOpenAIReasoningReplayE2E_SameProviderDifferentModelHandoff(t *testing.T) {
	requirePIAIE2E(t)
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY is not set")
	}
	sourceModel := openAIReasoningSourceModel()
	targetModel := openAIReasoningTargetModel()
	providers.ResetAPIProviders()

	context := ai.Context{
		SystemPrompt: "You are a helpful assistant. Answer concisely.",
		Tools:        []ai.Tool{doubleNumberTool()},
		Messages: []ai.Message{
			{
				Role:      ai.RoleUser,
				Text:      "Use the double_number tool to double 21.",
				Timestamp: time.Now().UnixMilli(),
			},
			{
				Role: ai.RoleAssistant,
				Content: []ai.ContentBlock{
					{
						Type:              ai.ContentTypeThinking,
						Thinking:          "I should call the tool first.",
						ThinkingSignature: `{"type":"reasoning","id":"rs_abc","summary":[{"type":"summary_text","text":"call tool"}]}`,
					},
					{
						Type:      ai.ContentTypeToolCall,
						ID:        "call_123|fc_456",
						Name:      "double_number",
						Arguments: map[string]any{"value": 21},
					},
				},
				Provider:   sourceModel.Provider,
				API:        sourceModel.API,
				Model:      sourceModel.ID,
				StopReason: ai.StopReasonToolUse,
				Timestamp:  time.Now().UnixMilli(),
			},
			{
				Role:       ai.RoleToolResult,
				ToolCallID: "call_123|fc_456",
				ToolName:   "double_number",
				Content: []ai.ContentBlock{
					{Type: ai.ContentTypeText, Text: "42"},
				},
				Timestamp: time.Now().UnixMilli(),
			},
			{
				Role:      ai.RoleUser,
				Text:      "What was the result? Answer with just the number.",
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}

	response, err := ai.CompleteSimple(targetModel, context, &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			APIKey:    apiKey,
			MaxTokens: 256,
		},
		Reasoning: ai.ThinkingHigh,
	})
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if response.StopReason == ai.StopReasonError {
		t.Fatalf("expected no provider error, got %q", response.ErrorMessage)
	}
	text := strings.ToLower(strings.TrimSpace(firstText(response)))
	if text == "" {
		t.Fatalf("expected non-empty text response")
	}
	if !strings.Contains(text, "42") &&
		!strings.Contains(text, "forty-two") &&
		!strings.Contains(text, "forty two") {
		t.Fatalf("expected handoff response to reference tool result, got %q", text)
	}
}

func openAIReasoningSourceModel() ai.Model {
	modelID := strings.TrimSpace(os.Getenv("PI_AI_E2E_OPENAI_REASONING_SOURCE_MODEL"))
	if modelID == "" {
		modelID = "gpt-5-mini"
	}
	baseURL := strings.TrimSpace(os.Getenv("PI_AI_E2E_OPENAI_BASE_URL"))
	return ai.Model{
		ID:        modelID,
		Name:      modelID,
		API:       ai.APIOpenAIResponses,
		Provider:  "openai",
		BaseURL:   baseURL,
		Reasoning: true,
	}
}

func openAIReasoningTargetModel() ai.Model {
	modelID := strings.TrimSpace(os.Getenv("PI_AI_E2E_OPENAI_REASONING_TARGET_MODEL"))
	if modelID == "" {
		modelID = "gpt-5.2-codex"
	}
	baseURL := strings.TrimSpace(os.Getenv("PI_AI_E2E_OPENAI_BASE_URL"))
	return ai.Model{
		ID:        modelID,
		Name:      modelID,
		API:       ai.APIOpenAIResponses,
		Provider:  "openai",
		BaseURL:   baseURL,
		Reasoning: true,
	}
}

func doubleNumberTool() ai.Tool {
	return ai.Tool{
		Name:        "double_number",
		Description: "Doubles a number and returns the result",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "number"},
			},
			"required": []any{"value"},
		},
	}
}
