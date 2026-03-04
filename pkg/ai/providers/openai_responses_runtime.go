package providers

import (
	"context"
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

func streamOpenAIResponses(model ai.Model, c ai.Context, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	openAIOptions := OpenAIResponsesOptions{}
	if options != nil {
		openAIOptions.StreamOptions = *options
	}
	return streamOpenAIResponsesWithOptions(model, c, openAIOptions)
}

func streamSimpleOpenAIResponses(model ai.Model, c ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	base := BuildBaseOptions(model, options, "")
	var effort ai.ThinkingLevel
	if options != nil {
		effort = options.Reasoning
	}
	if !ai.SupportsXhigh(model) {
		effort = ClampReasoning(effort)
	}
	return streamOpenAIResponsesWithOptions(model, c, OpenAIResponsesOptions{
		StreamOptions:   base,
		ReasoningEffort: effort,
	})
}

func streamOpenAIResponsesWithOptions(
	model ai.Model,
	c ai.Context,
	options OpenAIResponsesOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(128)
	go func() {
		apiKey := strings.TrimSpace(options.StreamOptions.APIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(ai.GetEnvAPIKey(string(model.Provider)))
		}
		if apiKey == "" {
			pushProviderError(stream, model, "missing API key for OpenAI responses runtime")
			return
		}

		payload := BuildOpenAIResponsesParams(model, c, options)
		if options.StreamOptions.OnPayload != nil {
			options.StreamOptions.OnPayload(payload)
		}

		request := param.Override[responses.ResponseNewParams](payload)
		reqOptions := []option.RequestOption{option.WithAPIKey(apiKey)}
		if baseURL := strings.TrimSpace(model.BaseURL); baseURL != "" {
			reqOptions = append(reqOptions, option.WithBaseURL(baseURL))
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
			pushProviderError(stream, model, "failed to create OpenAI responses stream")
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
				stream.Push(ai.AssistantMessageEvent{
					Type:  ai.EventTextDelta,
					Delta: event.Delta,
				})
			case "response.reasoning_text.delta":
				thinkingBuilder.WriteString(event.Delta)
				stream.Push(ai.AssistantMessageEvent{
					Type:  ai.EventThinkingDelta,
					Delta: event.Delta,
				})
			case "response.function_call_arguments.done":
				toolCall := ai.ContentBlock{
					Type:      ai.ContentTypeToolCall,
					ID:        strings.TrimSpace(event.ItemID),
					Name:      strings.TrimSpace(event.Name),
					Arguments: parseToolArguments(event.Arguments),
				}
				toolCalls = append(toolCalls, toolCall)
				stream.Push(ai.AssistantMessageEvent{
					Type:     ai.EventToolCallEnd,
					ToolCall: &toolCall,
				})
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
		}
		assistantMessage.Usage = ai.Usage{
			Input:       int(completedResponse.Usage.InputTokens),
			Output:      int(completedResponse.Usage.OutputTokens),
			TotalTokens: int(completedResponse.Usage.TotalTokens),
		}
		assistantMessage.Usage.Cost = ai.CalculateCost(model, assistantMessage.Usage)

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
		stream.Push(ai.AssistantMessageEvent{
			Type:    ai.EventDone,
			Message: assistantMessage,
			Reason:  assistantMessage.StopReason,
		})
	}()
	return stream
}

func parseToolArguments(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	args := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return map[string]any{"_raw": raw}
	}
	return args
}

func pushProviderError(stream *ai.AssistantMessageEventStream, model ai.Model, errText string) {
	if strings.TrimSpace(errText) == "" {
		errText = "openai responses stream failed"
	}
	stream.Push(ai.AssistantMessageEvent{
		Type: ai.EventError,
		Error: ai.Message{
			Role:         ai.RoleAssistant,
			API:          model.API,
			Provider:     model.Provider,
			Model:        model.ID,
			StopReason:   ai.StopReasonError,
			ErrorMessage: strings.TrimSpace(errText),
			Timestamp:    time.Now().UnixMilli(),
		},
		Reason: ai.StopReasonError,
	})
}

func mapOpenAIResponseStatus(status responses.ResponseStatus) ai.StopReason {
	switch status {
	case responses.ResponseStatusCompleted:
		return ai.StopReasonStop
	case responses.ResponseStatusInProgress, responses.ResponseStatusIncomplete:
		return ai.StopReasonLength
	case responses.ResponseStatusCancelled:
		return ai.StopReasonAborted
	case responses.ResponseStatusFailed:
		return ai.StopReasonError
	default:
		if strings.TrimSpace(string(status)) == "" {
			return ai.StopReasonStop
		}
		return ai.StopReasonStop
	}
}
