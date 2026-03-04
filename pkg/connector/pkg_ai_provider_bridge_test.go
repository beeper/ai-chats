package connector

import (
	"context"
	"errors"
	"testing"
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
		Model:               "gpt-4.1-mini",
		MaxCompletionTokens: 16384,
	}, "")
	if openAI.API != "openai-responses" {
		t.Fatalf("expected openai to map to openai-responses API, got %q", openAI.API)
	}
	if openAI.MaxTokens != 16384 {
		t.Fatalf("unexpected max tokens: %d", openAI.MaxTokens)
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

func TestTryGenerateStreamWithPkgAIFallsBackOnStubbedProviders(t *testing.T) {
	events, ok := tryGenerateStreamWithPkgAI(context.Background(), "https://openrouter.ai/api/v1", "", GenerateParams{
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
	if ok {
		t.Fatalf("expected fallback mode with stubbed providers, got events=%v", events)
	}
}
