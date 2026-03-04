package providers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/beeper/ai-bridge/pkg/ai"
	"github.com/beeper/ai-bridge/pkg/shared/httputil"
)

func streamOpenAICompletions(model ai.Model, c ai.Context, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	openAIOptions := OpenAICompletionsOptions{}
	if options != nil {
		openAIOptions.StreamOptions = *options
	}
	return streamOpenAICompletionsWithOptions(model, c, openAIOptions)
}

func streamSimpleOpenAICompletions(model ai.Model, c ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	base := BuildBaseOptions(model, options, "")
	var effort ai.ThinkingLevel
	if options != nil {
		effort = options.Reasoning
	}
	if !ai.SupportsXhigh(model) {
		effort = ClampReasoning(effort)
	}
	return streamOpenAICompletionsWithOptions(model, c, OpenAICompletionsOptions{
		StreamOptions:   base,
		ReasoningEffort: effort,
	})
}

func streamOpenAICompletionsWithOptions(
	model ai.Model,
	c ai.Context,
	options OpenAICompletionsOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(128)
	go func() {
		apiKey := strings.TrimSpace(options.StreamOptions.APIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(ai.GetEnvAPIKey(string(model.Provider)))
		}
		if apiKey == "" {
			pushProviderError(stream, model, "missing API key for OpenAI completions runtime")
			return
		}

		payload := BuildOpenAICompletionsParams(model, c, options)
		if options.StreamOptions.OnPayload != nil {
			options.StreamOptions.OnPayload(payload)
		}

		request := param.Override[openai.ChatCompletionNewParams](payload)
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
		openAIStream := client.Chat.Completions.NewStreaming(runCtx, request)
		if openAIStream == nil {
			pushProviderError(stream, model, "failed to create OpenAI completions stream")
			return
		}

		var textBuilder strings.Builder
		toolStates := map[int]*toolCallState{}
		toolOrder := make([]int, 0, 2)
		usage := ai.Usage{}
		stopReason := ai.StopReasonStop
		toolEventsEmitted := false

		for openAIStream.Next() {
			chunk := openAIStream.Current()
			if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				usage = ai.Usage{
					Input:       int(chunk.Usage.PromptTokens),
					Output:      int(chunk.Usage.CompletionTokens),
					TotalTokens: int(chunk.Usage.TotalTokens),
				}
			}

			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					textBuilder.WriteString(choice.Delta.Content)
					stream.Push(ai.AssistantMessageEvent{
						Type:  ai.EventTextDelta,
						Delta: choice.Delta.Content,
					})
				}

				for _, toolDelta := range choice.Delta.ToolCalls {
					idx := int(toolDelta.Index)
					state, ok := toolStates[idx]
					if !ok {
						state = &toolCallState{
							ID: fmt.Sprintf("call_%d", idx),
						}
						toolStates[idx] = state
						toolOrder = append(toolOrder, idx)
					}
					if strings.TrimSpace(toolDelta.ID) != "" {
						state.ID = strings.TrimSpace(toolDelta.ID)
					}
					if strings.TrimSpace(toolDelta.Function.Name) != "" {
						state.Name = strings.TrimSpace(toolDelta.Function.Name)
					}
					if toolDelta.Function.Arguments != "" {
						state.Arguments.WriteString(toolDelta.Function.Arguments)
					}
				}

				if choice.FinishReason != "" {
					stopReason = mapChatCompletionFinishReason(string(choice.FinishReason))
				}
			}
		}

		if err := openAIStream.Err(); err != nil {
			pushProviderError(stream, model, err.Error())
			return
		}

		content := make([]ai.ContentBlock, 0, len(toolOrder)+1)
		if text := strings.TrimSpace(textBuilder.String()); text != "" {
			content = append(content, ai.ContentBlock{
				Type: ai.ContentTypeText,
				Text: text,
			})
		}

		for _, idx := range toolOrder {
			state := toolStates[idx]
			if state == nil || strings.TrimSpace(state.Name) == "" {
				continue
			}
			toolCall := ai.ContentBlock{
				Type:      ai.ContentTypeToolCall,
				ID:        state.ID,
				Name:      state.Name,
				Arguments: parseToolArguments(state.Arguments.String()),
			}
			content = append(content, toolCall)
			stream.Push(ai.AssistantMessageEvent{
				Type:     ai.EventToolCallEnd,
				ToolCall: &toolCall,
			})
			toolEventsEmitted = true
		}

		if toolEventsEmitted && stopReason == ai.StopReasonStop {
			stopReason = ai.StopReasonToolUse
		}
		usage.Cost = ai.CalculateCost(model, usage)
		assistantMessage := ai.Message{
			Role:       ai.RoleAssistant,
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			Content:    content,
			Usage:      usage,
			StopReason: stopReason,
			Timestamp:  time.Now().UnixMilli(),
		}
		stream.Push(ai.AssistantMessageEvent{
			Type:    ai.EventDone,
			Message: assistantMessage,
			Reason:  assistantMessage.StopReason,
		})
	}()
	return stream
}

type toolCallState struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

func mapChatCompletionFinishReason(reason string) ai.StopReason {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "stop":
		return ai.StopReasonStop
	case "length":
		return ai.StopReasonLength
	case "tool_calls", "tool":
		return ai.StopReasonToolUse
	case "content_filter", "error":
		return ai.StopReasonError
	default:
		return ai.StopReasonStop
	}
}
