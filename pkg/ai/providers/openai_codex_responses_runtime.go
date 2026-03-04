package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/shared/httputil"
)

const codexJWTClaimPath = "https://api.openai.com/auth"

func streamOpenAICodexResponses(model ai.Model, c ai.Context, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	codexOptions := OpenAICodexResponsesOptions{}
	if options != nil {
		codexOptions.StreamOptions = *options
	}
	return streamOpenAICodexResponsesWithOptions(model, c, codexOptions)
}

func streamSimpleOpenAICodexResponses(model ai.Model, c ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	base := BuildBaseOptions(model, options, "")
	effort := ""
	if options != nil && options.Reasoning != "" {
		reasoning := options.Reasoning
		if !ai.SupportsXhigh(model) {
			reasoning = ClampReasoning(reasoning)
		}
		effort = string(reasoning)
	}
	return streamOpenAICodexResponsesWithOptions(model, c, OpenAICodexResponsesOptions{
		StreamOptions:   base,
		ReasoningEffort: effort,
	})
}

func streamOpenAICodexResponsesWithOptions(
	model ai.Model,
	c ai.Context,
	options OpenAICodexResponsesOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(128)
	go func() {
		apiKey := strings.TrimSpace(options.StreamOptions.APIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(ai.GetEnvAPIKey(string(model.Provider)))
		}
		if apiKey == "" {
			pushProviderError(stream, model, "missing API key for OpenAI Codex responses runtime")
			return
		}

		payload := BuildOpenAICodexResponsesParams(model, c, options)
		if options.StreamOptions.OnPayload != nil {
			options.StreamOptions.OnPayload(payload)
		}
		request := param.Override[responses.ResponseNewParams](payload)

		baseURL := resolveCodexSDKBaseURL(model.BaseURL)
		reqOptions := []option.RequestOption{
			option.WithAPIKey(apiKey),
			option.WithBaseURL(baseURL),
			option.WithHeader("OpenAI-Beta", "responses=experimental"),
			option.WithHeader("originator", "pi"),
		}
		if accountID := extractCodexAccountID(apiKey); accountID != "" {
			reqOptions = append(reqOptions, option.WithHeader("chatgpt-account-id", accountID))
		}
		reqOptions = httputil.AppendHeaderOptions(reqOptions, model.Headers)
		reqOptions = httputil.AppendHeaderOptions(reqOptions, options.StreamOptions.Headers)

		client := openai.NewClient(reqOptions...)
		runCtx := options.StreamOptions.Ctx
		if runCtx == nil {
			runCtx = context.Background()
		}

		openAIStream := client.Responses.NewStreaming(runCtx, request)
		if openAIStream == nil {
			pushProviderError(stream, model, "failed to create OpenAI Codex responses stream")
			return
		}

		var textBuilder strings.Builder
		var thinkingBuilder strings.Builder
		toolCalls := make([]ai.ContentBlock, 0, 2)
		var completedResponse responses.Response

		for openAIStream.Next() {
			event := openAIStream.Current()
			switch event.Type {
			case "response.output_text.delta":
				textBuilder.WriteString(event.Delta)
				stream.Push(ai.AssistantMessageEvent{Type: ai.EventTextDelta, Delta: event.Delta})
			case "response.reasoning_text.delta":
				thinkingBuilder.WriteString(event.Delta)
				stream.Push(ai.AssistantMessageEvent{Type: ai.EventThinkingDelta, Delta: event.Delta})
			case "response.function_call_arguments.done":
				toolCall := ai.ContentBlock{
					Type:      ai.ContentTypeToolCall,
					ID:        strings.TrimSpace(event.ItemID),
					Name:      strings.TrimSpace(event.Name),
					Arguments: parseToolArguments(event.Arguments),
				}
				toolCalls = append(toolCalls, toolCall)
				stream.Push(ai.AssistantMessageEvent{Type: ai.EventToolCallEnd, ToolCall: &toolCall})
			case "response.completed":
				completedResponse = event.Response
			case "error":
				pushProviderError(stream, model, strings.TrimSpace(event.Message))
				return
			}
		}

		if isContextAborted(runCtx, nil) {
			pushProviderAborted(stream, model)
			return
		}
		if err := openAIStream.Err(); err != nil {
			if isContextAborted(runCtx, err) {
				pushProviderAborted(stream, model)
				return
			}
			pushProviderError(stream, model, err.Error())
			return
		}

		assistantMessage := ai.Message{
			Role:       ai.RoleAssistant,
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			Timestamp:  time.Now().UnixMilli(),
			StopReason: mapOpenAIResponseStatus(completedResponse.Status),
			Usage: ai.Usage{
				Input:       int(completedResponse.Usage.InputTokens),
				Output:      int(completedResponse.Usage.OutputTokens),
				TotalTokens: int(completedResponse.Usage.TotalTokens),
			},
		}
		if thinking := strings.TrimSpace(thinkingBuilder.String()); thinking != "" {
			assistantMessage.Content = append(assistantMessage.Content, ai.ContentBlock{
				Type:     ai.ContentTypeThinking,
				Thinking: thinking,
			})
		}
		if text := strings.TrimSpace(textBuilder.String()); text != "" {
			assistantMessage.Content = append(assistantMessage.Content, ai.ContentBlock{
				Type: ai.ContentTypeText,
				Text: text,
			})
		}
		if len(toolCalls) > 0 {
			assistantMessage.Content = append(assistantMessage.Content, toolCalls...)
		}
		if len(toolCalls) > 0 && assistantMessage.StopReason == ai.StopReasonStop {
			assistantMessage.StopReason = ai.StopReasonToolUse
		}
		assistantMessage.Usage.Cost = ai.CalculateCost(model, assistantMessage.Usage)

		stream.Push(ai.AssistantMessageEvent{
			Type:    ai.EventDone,
			Message: assistantMessage,
			Reason:  assistantMessage.StopReason,
		})
	}()
	return stream
}

func resolveCodexSDKBaseURL(baseURL string) string {
	resolved := strings.TrimSpace(baseURL)
	if resolved == "" {
		return strings.TrimRight(defaultCodexBaseURL, "/") + "/codex"
	}
	resolved = strings.TrimRight(resolved, "/")
	if strings.HasSuffix(resolved, "/codex/responses") {
		return strings.TrimSuffix(resolved, "/responses")
	}
	if strings.HasSuffix(resolved, "/codex") {
		return resolved
	}
	return resolved + "/codex"
}

func extractCodexAccountID(token string) string {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return ""
	}
	payload := parts[1]
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}
	claims := map[string]any{}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}
	authClaims, ok := claims[codexJWTClaimPath].(map[string]any)
	if !ok {
		return ""
	}
	accountID, _ := authClaims["chatgpt_account_id"].(string)
	return strings.TrimSpace(accountID)
}
