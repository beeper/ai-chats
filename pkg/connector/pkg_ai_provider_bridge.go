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
	case strings.Contains(lower, "api.anthropic.com"):
		return "anthropic"
	case strings.Contains(lower, "cloudcode-pa.googleapis.com"):
		return "google-gemini-cli"
	case strings.Contains(lower, "aiplatform.googleapis.com"), strings.Contains(lower, "vertex"):
		return "google-vertex"
	case strings.Contains(lower, "googleapis.com"), strings.Contains(lower, "generativelanguage.googleapis.com"):
		return "google"
	case strings.Contains(lower, "bedrock"):
		return "amazon-bedrock"
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
	api := inferAPIFromProviderModel(provider, modelID)
	return aipkg.Model{
		ID:        modelID,
		Name:      modelID,
		Provider:  aipkg.Provider(provider),
		API:       api,
		BaseURL:   strings.TrimSpace(baseURL),
		Reasoning: modelSupportsReasoning(modelID) || strings.TrimSpace(params.ReasoningEffort) != "",
		Input:     []string{"text"},
		MaxTokens: max(params.MaxCompletionTokens, 4096),
	}
}

func inferAPIFromProviderModel(provider string, modelID string) aipkg.Api {
	switch provider {
	case "openrouter":
		return aipkg.APIOpenAICompletions
	case "azure-openai-responses":
		return aipkg.APIAzureOpenAIResponse
	case "anthropic":
		return aipkg.APIAnthropicMessages
	case "google":
		return aipkg.APIGoogleGenerativeAI
	case "google-gemini-cli":
		return aipkg.APIGoogleGeminiCLI
	case "google-vertex":
		return aipkg.APIGoogleVertex
	case "amazon-bedrock":
		return aipkg.APIBedrockConverse
	}
	model := strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case strings.HasPrefix(model, "claude-"):
		return aipkg.APIAnthropicMessages
	case strings.HasPrefix(model, "gemini-"):
		return aipkg.APIGoogleGenerativeAI
	default:
		return aipkg.APIOpenAIResponses
	}
}

func modelSupportsReasoning(modelID string) bool {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	return strings.HasPrefix(modelID, "gpt-5") ||
		strings.HasPrefix(modelID, "o1") ||
		strings.HasPrefix(modelID, "o3") ||
		strings.Contains(modelID, "thinking")
}

func parseThinkingLevel(value string) aipkg.ThinkingLevel {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "minimal":
		return aipkg.ThinkingMinimal
	case "low":
		return aipkg.ThinkingLow
	case "medium":
		return aipkg.ThinkingMedium
	case "high":
		return aipkg.ThinkingHigh
	case "xhigh":
		return aipkg.ThinkingXHigh
	default:
		return ""
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
	apiKey string,
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
		APIKey:      strings.TrimSpace(apiKey),
	}

	var (
		stream *aipkg.AssistantMessageEventStream
		err    error
	)
	if reasoning := parseThinkingLevel(params.ReasoningEffort); reasoning != "" {
		stream, err = aipkg.StreamSimple(model, aiContext, &aipkg.SimpleStreamOptions{
			StreamOptions: *options,
			Reasoning:     reasoning,
		})
	} else {
		stream, err = aipkg.Stream(model, aiContext, options)
	}
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
