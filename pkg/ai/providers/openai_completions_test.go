package providers

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func boolPtr(v bool) *bool { return &v }

func TestBuildOpenAICompletionsParams_ToolChoiceAndStrict(t *testing.T) {
	model := ai.Model{
		ID:       "gpt-4o-mini",
		API:      ai.APIOpenAICompletions,
		Provider: "openai",
		BaseURL:  "https://api.openai.com/v1",
		Input:    []string{"text"},
	}
	context := ai.Context{
		Messages: []ai.Message{{Role: ai.RoleUser, Text: "Call ping with ok=true", Timestamp: 1}},
		Tools: []ai.Tool{{
			Name:        "ping",
			Description: "Ping tool",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ok": map[string]any{"type": "boolean"},
				},
			},
		}},
	}

	params := BuildOpenAICompletionsParams(model, context, OpenAICompletionsOptions{
		ToolChoice: "required",
	})
	if params["tool_choice"] != "required" {
		t.Fatalf("expected tool_choice=required, got %v", params["tool_choice"])
	}
	tools, ok := params["tools"].([]map[string]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("expected non-empty tools payload")
	}
	fn, _ := tools[0]["function"].(map[string]any)
	if _, ok := fn["strict"]; !ok {
		t.Fatalf("expected strict in function payload when supported")
	}

	model.Compat = &ai.OpenAICompletionsCompat{
		SupportsStrictMode: boolPtr(false),
	}
	params = BuildOpenAICompletionsParams(model, context, OpenAICompletionsOptions{})
	tools, ok = params["tools"].([]map[string]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("expected non-empty tools payload")
	}
	fn, _ = tools[0]["function"].(map[string]any)
	if _, ok := fn["strict"]; ok {
		t.Fatalf("expected strict to be omitted when supportsStrictMode=false")
	}
}

func TestBuildOpenAICompletionsParams_ReasoningEffortGroqMapping(t *testing.T) {
	model := ai.Model{
		ID:       "qwen/qwen3-32b",
		API:      ai.APIOpenAICompletions,
		Provider: "groq",
		BaseURL:  "https://api.groq.com/openai/v1",
		Input:    []string{"text"},
	}
	context := ai.Context{
		Messages: []ai.Message{{Role: ai.RoleUser, Text: "Hi", Timestamp: 1}},
	}
	params := BuildOpenAICompletionsParams(model, context, OpenAICompletionsOptions{
		ReasoningEffort: ai.ThinkingMedium,
	})
	if params["reasoning_effort"] != "default" {
		t.Fatalf("expected reasoning_effort=default, got %v", params["reasoning_effort"])
	}

	model.ID = "openai/gpt-oss-20b"
	params = BuildOpenAICompletionsParams(model, context, OpenAICompletionsOptions{
		ReasoningEffort: ai.ThinkingMedium,
	})
	if params["reasoning_effort"] != "medium" {
		t.Fatalf("expected reasoning_effort=medium, got %v", params["reasoning_effort"])
	}
}
