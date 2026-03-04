package providers

import (
	"context"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	anthropicparam "github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/beeper/ai-bridge/pkg/ai"
)

func streamAnthropicMessages(model ai.Model, c ai.Context, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	anthropicOptions := AnthropicOptions{}
	if options != nil {
		anthropicOptions.StreamOptions = *options
	}
	return streamAnthropicMessagesWithOptions(model, c, anthropicOptions)
}

func streamSimpleAnthropicMessages(model ai.Model, c ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	base := BuildBaseOptions(model, options, "")
	if options == nil || options.Reasoning == "" {
		return streamAnthropicMessagesWithOptions(model, c, AnthropicOptions{
			StreamOptions:   base,
			ThinkingEnabled: false,
		})
	}
	if supportsAdaptiveThinkingModel(model.ID) {
		return streamAnthropicMessagesWithOptions(model, c, AnthropicOptions{
			StreamOptions:   base,
			ThinkingEnabled: true,
			Effort:          mapAnthropicThinkingEffort(model.ID, options.Reasoning),
		})
	}

	adjustedMaxTokens, thinkingBudget := AdjustMaxTokensForThinking(
		base.MaxTokens,
		model.MaxTokens,
		options.Reasoning,
		options.ThinkingBudgets,
	)
	base.MaxTokens = adjustedMaxTokens
	return streamAnthropicMessagesWithOptions(model, c, AnthropicOptions{
		StreamOptions:        base,
		ThinkingEnabled:      true,
		ThinkingBudgetTokens: thinkingBudget,
	})
}

func streamAnthropicMessagesWithOptions(
	model ai.Model,
	c ai.Context,
	options AnthropicOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(128)
	go func() {
		apiKey := strings.TrimSpace(options.StreamOptions.APIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(ai.GetEnvAPIKey(string(model.Provider)))
		}
		if apiKey == "" {
			pushProviderError(stream, model, "missing API key for Anthropic messages runtime")
			return
		}

		payload := BuildAnthropicParams(model, c, options)
		if options.StreamOptions.OnPayload != nil {
			options.StreamOptions.OnPayload(payload)
		}
		betaHeader := ""
		if rawBeta, ok := payload["anthropic-beta"].(string); ok {
			betaHeader = strings.TrimSpace(rawBeta)
			delete(payload, "anthropic-beta")
		}
		request := anthropicparam.Override[anthropic.MessageNewParams](payload)

		reqOptions := []anthropicoption.RequestOption{}
		if isOAuthAnthropicToken(apiKey) || model.Provider == "github-copilot" {
			reqOptions = append(reqOptions, anthropicoption.WithAuthToken(apiKey))
		} else {
			reqOptions = append(reqOptions, anthropicoption.WithAPIKey(apiKey))
		}
		if baseURL := strings.TrimSpace(model.BaseURL); baseURL != "" {
			reqOptions = append(reqOptions, anthropicoption.WithBaseURL(baseURL))
		}
		if betaHeader != "" {
			reqOptions = append(reqOptions, anthropicoption.WithHeader("anthropic-beta", betaHeader))
		}
		reqOptions = appendAnthropicHeaderOptions(reqOptions, model.Headers)
		reqOptions = appendAnthropicHeaderOptions(reqOptions, options.StreamOptions.Headers)

		client := anthropic.NewClient(reqOptions...)
		runCtx := options.StreamOptions.Ctx
		if runCtx == nil {
			runCtx = context.Background()
		}

		anthropicStream := client.Messages.NewStreaming(runCtx, request)
		if anthropicStream == nil {
			pushProviderError(stream, model, "failed to create Anthropic messages stream")
			return
		}

		accumulated := anthropic.Message{}
		for anthropicStream.Next() {
			event := anthropicStream.Current()
			if err := (&accumulated).Accumulate(event); err != nil {
				pushProviderError(stream, model, err.Error())
				return
			}

			switch eventVariant := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
				switch deltaVariant := eventVariant.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					stream.Push(ai.AssistantMessageEvent{
						Type:  ai.EventTextDelta,
						Delta: deltaVariant.Text,
					})
				case anthropic.ThinkingDelta:
					stream.Push(ai.AssistantMessageEvent{
						Type:  ai.EventThinkingDelta,
						Delta: deltaVariant.Thinking,
					})
				}
			case anthropic.ContentBlockStopEvent:
				contentIndex := int(eventVariant.Index)
				if contentIndex < 0 || contentIndex >= len(accumulated.Content) {
					continue
				}
				if toolUse, ok := accumulated.Content[contentIndex].AsAny().(anthropic.ToolUseBlock); ok {
					toolCall := ai.ContentBlock{
						Type:      ai.ContentTypeToolCall,
						ID:        strings.TrimSpace(toolUse.ID),
						Name:      strings.TrimSpace(toolUse.Name),
						Arguments: parseToolArguments(string(toolUse.Input)),
					}
					stream.Push(ai.AssistantMessageEvent{
						Type:         ai.EventToolCallEnd,
						ContentIndex: contentIndex,
						ToolCall:     &toolCall,
					})
				}
			}
		}

		if err := anthropicStream.Err(); err != nil {
			pushProviderError(stream, model, err.Error())
			return
		}

		assistantMessage := anthropicMessageToAIMessage(model, accumulated)
		stream.Push(ai.AssistantMessageEvent{
			Type:    ai.EventDone,
			Message: assistantMessage,
			Reason:  assistantMessage.StopReason,
		})
	}()
	return stream
}

func anthropicMessageToAIMessage(model ai.Model, msg anthropic.Message) ai.Message {
	out := ai.Message{
		Role:       ai.RoleAssistant,
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		Timestamp:  time.Now().UnixMilli(),
		StopReason: mapAnthropicStopReason(msg.StopReason),
		Usage: ai.Usage{
			Input:      int(msg.Usage.InputTokens),
			Output:     int(msg.Usage.OutputTokens),
			CacheRead:  int(msg.Usage.CacheReadInputTokens),
			CacheWrite: int(msg.Usage.CacheCreationInputTokens),
		},
	}

	for _, block := range msg.Content {
		switch blockVariant := block.AsAny().(type) {
		case anthropic.TextBlock:
			if strings.TrimSpace(blockVariant.Text) == "" {
				continue
			}
			out.Content = append(out.Content, ai.ContentBlock{
				Type: ai.ContentTypeText,
				Text: blockVariant.Text,
			})
		case anthropic.ThinkingBlock:
			if strings.TrimSpace(blockVariant.Thinking) == "" {
				continue
			}
			out.Content = append(out.Content, ai.ContentBlock{
				Type:              ai.ContentTypeThinking,
				Thinking:          blockVariant.Thinking,
				ThinkingSignature: blockVariant.Signature,
			})
		case anthropic.RedactedThinkingBlock:
			out.Content = append(out.Content, ai.ContentBlock{
				Type:     ai.ContentTypeThinking,
				Thinking: blockVariant.Data,
				Redacted: true,
			})
		case anthropic.ToolUseBlock:
			out.Content = append(out.Content, ai.ContentBlock{
				Type:      ai.ContentTypeToolCall,
				ID:        strings.TrimSpace(blockVariant.ID),
				Name:      strings.TrimSpace(blockVariant.Name),
				Arguments: parseToolArguments(string(blockVariant.Input)),
			})
		}
	}

	out.Usage.TotalTokens = out.Usage.Input + out.Usage.Output + out.Usage.CacheRead + out.Usage.CacheWrite
	out.Usage.Cost = ai.CalculateCost(model, out.Usage)
	if out.StopReason == ai.StopReasonStop {
		for _, block := range out.Content {
			if block.Type == ai.ContentTypeToolCall {
				out.StopReason = ai.StopReasonToolUse
				break
			}
		}
	}
	return out
}

func appendAnthropicHeaderOptions(
	opts []anthropicoption.RequestOption,
	headers map[string]string,
) []anthropicoption.RequestOption {
	for key, value := range headers {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		opts = append(opts, anthropicoption.WithHeader(key, trimmed))
	}
	return opts
}

func isOAuthAnthropicToken(apiKey string) bool {
	return strings.Contains(apiKey, "sk-ant-oat")
}

func supportsAdaptiveThinkingModel(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	return strings.Contains(id, "opus-4-6") || strings.Contains(id, "opus-4.6") ||
		strings.Contains(id, "sonnet-4-6") || strings.Contains(id, "sonnet-4.6")
}

func mapAnthropicThinkingEffort(modelID string, level ai.ThinkingLevel) string {
	switch level {
	case ai.ThinkingMinimal, ai.ThinkingLow:
		return "low"
	case ai.ThinkingMedium:
		return "medium"
	case ai.ThinkingHigh:
		return "high"
	case ai.ThinkingXHigh:
		id := strings.ToLower(strings.TrimSpace(modelID))
		if strings.Contains(id, "opus-4-6") || strings.Contains(id, "opus-4.6") {
			return "max"
		}
		return "high"
	default:
		return "high"
	}
}

func mapAnthropicStopReason(reason anthropic.StopReason) ai.StopReason {
	switch reason {
	case anthropic.StopReasonMaxTokens:
		return ai.StopReasonLength
	case anthropic.StopReasonToolUse:
		return ai.StopReasonToolUse
	case anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence:
		return ai.StopReasonStop
	default:
		return ai.StopReasonStop
	}
}
