package connector

import (
	"context"
	"os"
	"strings"
	"time"

	aipkg "github.com/beeper/ai-bridge/pkg/ai"
	aiproviders "github.com/beeper/ai-bridge/pkg/ai/providers"
)

func pkgAIProviderRuntimeEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PI_USE_PKG_AI_PROVIDER_RUNTIME")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func inferProviderNameFromBaseURL(baseURL string) string {
	lower := strings.ToLower(strings.TrimSpace(baseURL))
	switch {
	case strings.Contains(lower, "openrouter.ai"):
		return "openrouter"
	case strings.Contains(lower, "beeper.com"):
		return "beeper"
	case strings.Contains(lower, "magicproxy"):
		return "magic-proxy"
	case strings.Contains(lower, "azure.com"):
		return "azure-openai-responses"
	default:
		return "openai"
	}
}

func buildPkgAIModelFromGenerateParams(params GenerateParams, baseURL string) aipkg.Model {
	modelID := strings.TrimSpace(params.Model)
	provider := inferProviderNameFromBaseURL(baseURL)
	api := aipkg.APIOpenAIResponses
	if provider == "openrouter" {
		api = aipkg.APIOpenAICompletions
	}
	return aipkg.Model{
		ID:        modelID,
		Name:      modelID,
		Provider:  aipkg.Provider(provider),
		API:       api,
		BaseURL:   strings.TrimSpace(baseURL),
		Input:     []string{"text"},
		MaxTokens: max(params.MaxCompletionTokens, 4096),
	}
}

func shouldFallbackFromPkgAIEvent(event StreamEvent) bool {
	if event.Type != StreamEventError || event.Error == nil {
		return false
	}
	errText := strings.ToLower(strings.TrimSpace(event.Error.Error()))
	return strings.Contains(errText, "not implemented yet") ||
		strings.Contains(errText, "no api provider registered")
}

func tryGenerateStreamWithPkgAI(
	ctx context.Context,
	baseURL string,
	params GenerateParams,
) (<-chan StreamEvent, bool) {
	aiproviders.RegisterBuiltInAPIProviders()
	model := buildPkgAIModelFromGenerateParams(params, baseURL)
	aiContext := toAIContext(params.SystemPrompt, params.Messages, params.Tools)

	temp := params.Temperature
	options := &aipkg.StreamOptions{
		Ctx:         ctx,
		MaxTokens:   params.MaxCompletionTokens,
		Temperature: &temp,
	}

	stream, err := aipkg.Stream(model, aiContext, options)
	if err != nil {
		return nil, false
	}

	mapped := streamEventsFromAIStream(ctx, stream)
	select {
	case first, ok := <-mapped:
		if !ok {
			return nil, false
		}
		if shouldFallbackFromPkgAIEvent(first) {
			return nil, false
		}
		out := make(chan StreamEvent, 64)
		go func() {
			defer close(out)
			out <- first
			for event := range mapped {
				out <- event
			}
		}()
		return out, true
	case <-time.After(50 * time.Millisecond):
		// No immediate events: proceed with pkg/ai channel and let caller consume.
		return mapped, true
	}
}
