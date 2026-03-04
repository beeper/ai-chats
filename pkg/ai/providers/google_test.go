package providers

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func TestBuildGoogleGenerateContentParams(t *testing.T) {
	temp := 0.4
	budget := 2048
	params := BuildGoogleGenerateContentParams(
		ai.Model{
			ID:        "gemini-2.5-flash",
			Provider:  "google",
			API:       ai.APIGoogleGenerativeAI,
			Reasoning: true,
			Input:     []string{"text"},
		},
		ai.Context{
			SystemPrompt: "You are helpful",
			Messages: []ai.Message{
				{Role: ai.RoleUser, Text: "hello"},
			},
			Tools: []ai.Tool{
				{Name: "search", Description: "Search", Parameters: map[string]any{"type": "object"}},
			},
		},
		GoogleOptions{
			StreamOptions: ai.StreamOptions{
				Temperature: &temp,
				MaxTokens:   2048,
			},
			ToolChoice: "any",
			Thinking: &GoogleThinkingOptions{
				Enabled:      true,
				BudgetTokens: &budget,
			},
		},
	)
	if params["model"] != "gemini-2.5-flash" {
		t.Fatalf("expected model id in params")
	}
	config, ok := params["config"].(map[string]any)
	if !ok {
		t.Fatalf("expected config payload")
	}
	if config["temperature"] != 0.4 || config["maxOutputTokens"] != 2048 {
		t.Fatalf("unexpected generation config: %#v", config)
	}
	toolConfig, ok := config["toolConfig"].(map[string]any)
	if !ok {
		t.Fatalf("expected toolConfig when tools+toolChoice are present")
	}
	mode := toolConfig["functionCallingConfig"].(map[string]any)["mode"]
	if mode != "ANY" {
		t.Fatalf("expected ANY tool mode, got %#v", mode)
	}
	thinking := config["thinkingConfig"].(map[string]any)
	if thinking["includeThoughts"] != true || thinking["thinkingBudget"] != 2048 {
		t.Fatalf("unexpected thinking config: %#v", thinking)
	}
}
