package connector

import (
	"context"
	"errors"
	"testing"

	aipkg "github.com/beeper/ai-bridge/pkg/ai"
)

func TestPkgAIProviderRuntimeEnabled(t *testing.T) {
	t.Setenv("PI_USE_PKG_AI_PROVIDER_RUNTIME", "true")
	if !pkgAIProviderRuntimeEnabled() {
		t.Fatalf("expected runtime flag to be enabled")
	}

	t.Setenv("PI_USE_PKG_AI_PROVIDER_RUNTIME", "0")
	if pkgAIProviderRuntimeEnabled() {
		t.Fatalf("expected runtime flag to be disabled")
	}
}

func TestInferProviderNameFromBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "default", baseURL: "", want: "openai"},
		{name: "openrouter", baseURL: "https://openrouter.ai/api/v1", want: "openrouter"},
		{name: "beeper proxy", baseURL: "https://ai.beeper.com/openai", want: "beeper"},
		{name: "magic proxy", baseURL: "https://magicproxy.example/v1", want: "magic-proxy"},
		{name: "azure", baseURL: "https://my-openai.azure.com", want: "azure-openai-responses"},
		{name: "anthropic", baseURL: "https://api.anthropic.com", want: "anthropic"},
		{name: "google cloudcode", baseURL: "https://cloudcode-pa.googleapis.com", want: "google-gemini-cli"},
		{name: "google antigravity", baseURL: "https://daily-cloudcode-pa.sandbox.googleapis.com", want: "google-gemini-cli"},
		{name: "google mldev", baseURL: "https://generativelanguage.googleapis.com", want: "google"},
		{name: "google vertex", baseURL: "https://us-central1-aiplatform.googleapis.com", want: "google-vertex"},
		{name: "bedrock", baseURL: "https://bedrock-runtime.us-east-1.amazonaws.com", want: "amazon-bedrock"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferProviderNameFromBaseURL(tc.baseURL)
			if got != tc.want {
				t.Fatalf("inferProviderNameFromBaseURL(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
}

func TestBuildPkgAIModelFromGenerateParams(t *testing.T) {
	openRouter := buildPkgAIModelFromGenerateParams(GenerateParams{
		Model:               "openai/gpt-4o-mini",
		MaxCompletionTokens: 256,
	}, "https://openrouter.ai/api/v1")
	if openRouter.API != "openai-completions" {
		t.Fatalf("expected openrouter to map to openai-completions API, got %q", openRouter.API)
	}
	if openRouter.MaxTokens != 4096 {
		t.Fatalf("expected minimum max tokens guard, got %d", openRouter.MaxTokens)
	}

	openAI := buildPkgAIModelFromGenerateParams(GenerateParams{
		Model:               "gpt-5-mini",
		MaxCompletionTokens: 16384,
	}, "")
	if openAI.API != "openai-responses" {
		t.Fatalf("expected openai to map to openai-responses API, got %q", openAI.API)
	}
	if openAI.MaxTokens != 16384 {
		t.Fatalf("unexpected max tokens: %d", openAI.MaxTokens)
	}
	if !openAI.Reasoning {
		t.Fatalf("expected gpt-5 family model to be marked as reasoning capable")
	}

	azure := buildPkgAIModelFromGenerateParams(GenerateParams{
		Model: "gpt-4.1-mini",
	}, "https://my-openai.azure.com")
	if azure.API != "azure-openai-responses" {
		t.Fatalf("expected azure base URL to map to azure-openai-responses API, got %q", azure.API)
	}

	anthropic := buildPkgAIModelFromGenerateParams(GenerateParams{
		Model: "claude-sonnet-4-5",
	}, "https://api.anthropic.com")
	if anthropic.API != "anthropic-messages" {
		t.Fatalf("expected anthropic base URL to map to anthropic-messages API, got %q", anthropic.API)
	}

	google := buildPkgAIModelFromGenerateParams(GenerateParams{
		Model: "gemini-2.5-flash",
	}, "https://generativelanguage.googleapis.com")
	if google.API != "google-generative-ai" {
		t.Fatalf("expected google base URL to map to google-generative-ai API, got %q", google.API)
	}

	antigravity := buildPkgAIModelFromGenerateParams(GenerateParams{
		Model: "gemini-2.5-pro",
	}, "https://daily-cloudcode-pa.sandbox.googleapis.com")
	if antigravity.API != "google-gemini-cli" {
		t.Fatalf("expected antigravity endpoint to map to google-gemini-cli API, got %q", antigravity.API)
	}

	bedrock := buildPkgAIModelFromGenerateParams(GenerateParams{
		Model: "us.anthropic.claude-3-5-sonnet-20241022-v2:0",
	}, "https://bedrock-runtime.us-east-1.amazonaws.com")
	if bedrock.API != aipkg.APIBedrockConverse {
		t.Fatalf("expected bedrock base URL to map to %q API, got %q", aipkg.APIBedrockConverse, bedrock.API)
	}

	nonReasoning := buildPkgAIModelFromGenerateParams(GenerateParams{
		Model: "gpt-4.1-mini",
	}, "")
	if nonReasoning.Reasoning {
		t.Fatalf("did not expect non-reasoning model to be marked as reasoning capable")
	}

	withReasoningOverride := buildPkgAIModelFromGenerateParams(GenerateParams{
		Model:           "gpt-4.1-mini",
		ReasoningEffort: "high",
	}, "")
	if !withReasoningOverride.Reasoning {
		t.Fatalf("expected reasoning effort override to mark model as reasoning capable")
	}

	heuristicAnthropic := buildPkgAIModelFromGenerateParams(GenerateParams{
		Model: "claude-3-7-sonnet-latest",
	}, "")
	if heuristicAnthropic.API != "anthropic-messages" {
		t.Fatalf("expected claude model heuristic to map to anthropic API, got %q", heuristicAnthropic.API)
	}

	heuristicGoogle := buildPkgAIModelFromGenerateParams(GenerateParams{
		Model: "gemini-2.5-pro",
	}, "")
	if heuristicGoogle.API != "google-generative-ai" {
		t.Fatalf("expected gemini model heuristic to map to google API, got %q", heuristicGoogle.API)
	}
}

func TestShouldFallbackFromPkgAIEvent(t *testing.T) {
	if !shouldFallbackFromPkgAIEvent(StreamEvent{
		Type:  StreamEventError,
		Error: errors.New("provider runtime is not implemented yet"),
	}) {
		t.Fatalf("expected not-implemented errors to trigger fallback")
	}
	if shouldFallbackFromPkgAIEvent(StreamEvent{Type: StreamEventDelta, Delta: "ok"}) {
		t.Fatalf("did not expect non-error events to trigger fallback")
	}
}

func TestShouldFallbackFromPkgAIError(t *testing.T) {
	if !shouldFallbackFromPkgAIError(errors.New("provider runtime is not implemented yet")) {
		t.Fatalf("expected not-implemented errors to trigger fallback")
	}
	if !shouldFallbackFromPkgAIError(errors.New("provider x has no stream function")) {
		t.Fatalf("expected missing stream function errors to trigger fallback")
	}
	if shouldFallbackFromPkgAIError(errors.New("missing API key for provider")) {
		t.Fatalf("did not expect runtime credential errors to trigger fallback")
	}
}

func TestTryGenerateStreamWithPkgAIReturnsRuntimeErrorEventsWhenProviderResolved(t *testing.T) {
	events, ok := tryGenerateStreamWithPkgAI(context.Background(), "https://my-openai.azure.com", "", GenerateParams{
		Model: "gpt-4.1-mini",
		Messages: []UnifiedMessage{
			{
				Role: RoleUser,
				Content: []ContentPart{
					{Type: ContentTypeText, Text: "hello"},
				},
			},
		},
	})
	if !ok {
		t.Fatalf("expected pkg/ai stream to be selected")
	}
	event := <-events
	if event.Type != StreamEventError {
		t.Fatalf("expected runtime error event without credentials, got %#v", event)
	}
}

func TestTryGenerateWithPkgAIReturnsRuntimeErrorForGeminiCLI(t *testing.T) {
	resp, handled, err := tryGenerateWithPkgAI(context.Background(), "https://cloudcode-pa.googleapis.com", "", GenerateParams{
		Model: "gemini-2.5-flash",
		Messages: []UnifiedMessage{
			{
				Role: RoleUser,
				Content: []ContentPart{
					{Type: ContentTypeText, Text: "hello"},
				},
			},
		},
	})
	if !handled {
		t.Fatalf("expected gemini-cli provider to be handled by pkg/ai runtime")
	}
	if err == nil {
		t.Fatalf("expected runtime error without OAuth credentials")
	}
	if resp != nil {
		t.Fatalf("expected nil response when runtime returns error")
	}
}

func TestTryGenerateWithPkgAIReturnsRuntimeErrorWhenProviderResolved(t *testing.T) {
	resp, handled, err := tryGenerateWithPkgAI(context.Background(), "https://api.anthropic.com", "", GenerateParams{
		Model: "claude-sonnet-4-5",
		Messages: []UnifiedMessage{
			{
				Role: RoleUser,
				Content: []ContentPart{
					{Type: ContentTypeText, Text: "hello"},
				},
			},
		},
	})
	if !handled {
		t.Fatalf("expected anthropic provider to be handled by pkg/ai runtime")
	}
	if err == nil {
		t.Fatalf("expected runtime error without credentials")
	}
	if resp != nil {
		t.Fatalf("expected nil response when runtime returns error")
	}
}

func TestGenerateResponseFromAIMessage(t *testing.T) {
	resp := generateResponseFromAIMessage(aipkg.Message{
		StopReason: aipkg.StopReasonToolUse,
		Usage:      aipkg.Usage{Input: 7, Output: 3, TotalTokens: 10},
		Content: []aipkg.ContentBlock{
			{Type: aipkg.ContentTypeThinking, Thinking: "plan"},
			{Type: aipkg.ContentTypeText, Text: "answer"},
			{Type: aipkg.ContentTypeToolCall, ID: "call_1", Name: "search", Arguments: map[string]any{"q": "go"}},
		},
	})
	if resp.Content != "answer" {
		t.Fatalf("expected content text extraction, got %q", resp.Content)
	}
	if resp.FinishReason != string(aipkg.StopReasonToolUse) {
		t.Fatalf("unexpected finish reason: %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "search" {
		t.Fatalf("expected tool call mapping, got %#v", resp.ToolCalls)
	}
	if resp.Usage.TotalTokens != 10 {
		t.Fatalf("expected usage mapping, got %#v", resp.Usage)
	}
}

func TestParseThinkingLevel(t *testing.T) {
	cases := map[string]string{
		"minimal": "minimal",
		"low":     "low",
		"medium":  "medium",
		"high":    "high",
		"xhigh":   "xhigh",
		"none":    "",
		"":        "",
	}
	for in, want := range cases {
		if got := string(parseThinkingLevel(in)); got != want {
			t.Fatalf("parseThinkingLevel(%q) = %q, want %q", in, got, want)
		}
	}
}
