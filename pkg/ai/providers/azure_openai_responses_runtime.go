package providers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/shared/httputil"
)

func streamAzureOpenAIResponses(model ai.Model, c ai.Context, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	azureOptions := AzureOpenAIResponsesOptions{}
	if options != nil {
		azureOptions.StreamOptions = *options
	}
	return streamAzureOpenAIResponsesWithOptions(model, c, azureOptions)
}

func streamSimpleAzureOpenAIResponses(model ai.Model, c ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	base := BuildBaseOptions(model, options, "")
	var effort ai.ThinkingLevel
	if options != nil {
		effort = options.Reasoning
	}
	if !ai.SupportsXhigh(model) {
		effort = ClampReasoning(effort)
	}
	return streamAzureOpenAIResponsesWithOptions(model, c, AzureOpenAIResponsesOptions{
		OpenAIResponsesOptions: OpenAIResponsesOptions{
			StreamOptions:   base,
			ReasoningEffort: effort,
		},
	})
}

func streamAzureOpenAIResponsesWithOptions(
	model ai.Model,
	c ai.Context,
	options AzureOpenAIResponsesOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(128)
	go func() {
		baseURL, apiVersion, err := ResolveAzureConfig(model, &options)
		if err != nil {
			pushProviderError(stream, model, err.Error())
			return
		}

		apiKey := strings.TrimSpace(options.StreamOptions.APIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(ai.GetEnvAPIKey("azure-openai-responses"))
		}
		if apiKey == "" {
			pushProviderError(stream, model, "missing API key for Azure OpenAI responses runtime")
			return
		}

		payload := BuildAzureOpenAIResponsesParams(model, c, options)
		if options.StreamOptions.OnPayload != nil {
			options.StreamOptions.OnPayload(payload)
		}
		request := param.Override[responses.ResponseNewParams](payload)

		reqOptions := []option.RequestOption{
			option.WithAPIKey(apiKey),
			option.WithBaseURL(baseURL),
			option.WithHeader("api-key", apiKey),
		}
		if apiVersion != "" && apiVersion != "v1" {
			reqOptions = append(reqOptions, option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
				q := req.URL.Query()
				q.Set("api-version", apiVersion)
				req.URL.RawQuery = q.Encode()
				return next(req)
			}))
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
			pushProviderError(stream, model, "failed to create Azure OpenAI responses stream")
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
