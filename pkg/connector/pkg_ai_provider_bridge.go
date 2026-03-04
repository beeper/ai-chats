package connector

import (
	"context"
	"encoding/json"
	"errors"
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

func shouldFallbackFromPkgAIError(err error) bool {
	if err == nil {
		return false
	}
	errText := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(errText, "not implemented yet") ||
		strings.Contains(errText, "no api provider registered") ||
		strings.Contains(errText, "has no stream function") ||
		strings.Contains(errText, "has no streamsimple function")
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

func tryGenerateWithPkgAI(
	ctx context.Context,
	baseURL string,
	apiKey string,
	params GenerateParams,
) (*GenerateResponse, bool, error) {
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
		message aipkg.Message
		err     error
	)
	if reasoning := parseThinkingLevel(params.ReasoningEffort); reasoning != "" {
		message, err = aipkg.CompleteSimple(model, aiContext, &aipkg.SimpleStreamOptions{
			StreamOptions: *options,
			Reasoning:     reasoning,
		})
	} else {
		message, err = aipkg.Complete(model, aiContext, options)
	}
	if err != nil {
		if shouldFallbackFromPkgAIError(err) {
			return nil, false, nil
		}
		return nil, true, err
	}
	if message.StopReason == aipkg.StopReasonError && strings.TrimSpace(message.ErrorMessage) != "" {
		runtimeErr := errors.New(strings.TrimSpace(message.ErrorMessage))
		if shouldFallbackFromPkgAIError(runtimeErr) {
			return nil, false, nil
		}
		return nil, true, runtimeErr
	}
	return generateResponseFromAIMessage(message), true, nil
}

func generateResponseFromAIMessage(message aipkg.Message) *GenerateResponse {
	var contentParts []string
	var thinkingParts []string
	toolCalls := make([]ToolCallResult, 0)
	for _, block := range message.Content {
		switch block.Type {
		case aipkg.ContentTypeText:
			if text := strings.TrimSpace(block.Text); text != "" {
				contentParts = append(contentParts, text)
			}
		case aipkg.ContentTypeThinking:
			if thinking := strings.TrimSpace(block.Thinking); thinking != "" {
				thinkingParts = append(thinkingParts, thinking)
			}
		case aipkg.ContentTypeToolCall:
			argumentsJSON := "{}"
			if block.Arguments != nil {
				if raw, err := json.Marshal(block.Arguments); err == nil {
					argumentsJSON = string(raw)
				}
			}
			toolCalls = append(toolCalls, ToolCallResult{
				ID:        strings.TrimSpace(block.ID),
				Name:      strings.TrimSpace(block.Name),
				Arguments: argumentsJSON,
			})
		}
	}

	content := strings.Join(contentParts, "\n")
	if strings.TrimSpace(content) == "" && len(thinkingParts) > 0 {
		content = strings.Join(thinkingParts, "\n")
	}
	finishReason := strings.TrimSpace(string(message.StopReason))
	if finishReason == "" {
		finishReason = "stop"
	}

	return &GenerateResponse{
		Content:      content,
		FinishReason: finishReason,
		ToolCalls:    toolCalls,
		Usage: UsageInfo{
			PromptTokens:     message.Usage.Input,
			CompletionTokens: message.Usage.Output,
			TotalTokens:      message.Usage.TotalTokens,
		},
	}
}
