package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	bridgesdk "github.com/beeper/agentremote/sdk"
)

// reasoningEffortMap maps string effort levels to SDK constants.
var reasoningEffortMap = map[string]responses.ReasoningEffort{
	"low":    responses.ReasoningEffortLow,
	"medium": responses.ReasoningEffortMedium,
	"high":   responses.ReasoningEffortHigh,
}

// buildResponsesParams constructs Responses API parameters from GenerateParams.
func (o *OpenAIProvider) buildResponsesParams(params GenerateParams) responses.ResponseNewParams {
	responsesParams := responses.ResponseNewParams{
		Model: params.Model,
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: bridgesdk.PromptContextToResponsesInput(params.Context.PromptContext),
		},
	}

	if params.MaxCompletionTokens > 0 {
		responsesParams.MaxOutputTokens = openai.Int(int64(params.MaxCompletionTokens))
	}
	if params.Temperature != nil {
		responsesParams.Temperature = openai.Float(*params.Temperature)
	}
	if params.Context.SystemPrompt != "" {
		responsesParams.Instructions = openai.String(params.Context.SystemPrompt)
	}
	if len(params.Context.Tools) > 0 {
		strictMode := resolveToolStrictMode(isOpenRouterBaseURL(o.baseURL))
		responsesParams.Tools = ToOpenAITools(params.Context.Tools, strictMode, &o.log)
	}
	if effort, ok := reasoningEffortMap[params.ReasoningEffort]; ok {
		responsesParams.Reasoning = responses.ReasoningParam{
			Effort: effort,
		}
	}
	if strings.TrimSpace(params.PreviousResponseID) != "" {
		responsesParams.PreviousResponseID = openai.String(strings.TrimSpace(params.PreviousResponseID))
	}
	if params.WebSearchEnabled {
		responsesParams.Tools = append(responsesParams.Tools, responses.ToolUnionParam{
			OfWebSearch: &responses.WebSearchToolParam{},
		})
	}
	responsesParams.Tools = dedupeToolParams(responsesParams.Tools)
	return responsesParams
}

// GenerateStream generates a streaming response from OpenAI using the Responses API.
func (o *OpenAIProvider) GenerateStream(ctx context.Context, params GenerateParams) (<-chan StreamEvent, error) {
	if bridgesdk.HasUnsupportedResponsesPromptContext(params.Context.PromptContext) {
		return nil, fmt.Errorf("responses API does not support prompt context block types required by this request")
	}

	events := make(chan StreamEvent, 100)

	go func() {
		defer close(events)

		responsesParams := o.buildResponsesParams(params)
		stream := o.client.Responses.NewStreaming(ctx, responsesParams)
		if stream == nil {
			events <- StreamEvent{
				Type:  StreamEventError,
				Error: errors.New("failed to create streaming request"),
			}
			return
		}

		var responseID string
		for stream.Next() {
			streamEvent := stream.Current()

			switch streamEvent.Type {
			case "response.output_text.delta":
				events <- StreamEvent{
					Type:  StreamEventDelta,
					Delta: streamEvent.Delta,
				}
			case "response.reasoning_text.delta":
				events <- StreamEvent{
					Type:           StreamEventReasoning,
					ReasoningDelta: streamEvent.Delta,
				}
			case "response.function_call_arguments.done":
				events <- StreamEvent{
					Type: StreamEventToolCall,
					ToolCall: &ToolCallResult{
						ID:        streamEvent.ItemID,
						Name:      streamEvent.Name,
						Arguments: streamEvent.Arguments,
					},
				}
			case "response.completed":
				responseID = streamEvent.Response.ID
				finishReason := "stop"
				if streamEvent.Response.Status != "completed" {
					finishReason = string(streamEvent.Response.Status)
				}

				var usage *UsageInfo
				if streamEvent.Response.Usage.InputTokens > 0 || streamEvent.Response.Usage.OutputTokens > 0 {
					usage = &UsageInfo{
						PromptTokens:     int(streamEvent.Response.Usage.InputTokens),
						CompletionTokens: int(streamEvent.Response.Usage.OutputTokens),
						TotalTokens:      int(streamEvent.Response.Usage.TotalTokens),
					}
					if streamEvent.Response.Usage.OutputTokensDetails.ReasoningTokens > 0 {
						usage.ReasoningTokens = int(streamEvent.Response.Usage.OutputTokensDetails.ReasoningTokens)
					}
				}

				events <- StreamEvent{
					Type:         StreamEventComplete,
					FinishReason: finishReason,
					ResponseID:   responseID,
					Usage:        usage,
				}
			case "error":
				events <- StreamEvent{
					Type:  StreamEventError,
					Error: fmt.Errorf("API error: %s", streamEvent.Message),
				}
				return
			}
		}

		if err := stream.Err(); err != nil {
			events <- StreamEvent{
				Type:  StreamEventError,
				Error: err,
			}
		}
	}()

	return events, nil
}

// Generate performs a non-streaming generation using the Responses API.
func (o *OpenAIProvider) Generate(ctx context.Context, params GenerateParams) (*GenerateResponse, error) {
	if bridgesdk.HasUnsupportedResponsesPromptContext(params.Context.PromptContext) {
		return nil, fmt.Errorf("responses API does not support prompt context block types required by this request")
	}

	responsesParams := o.buildResponsesParams(params)
	resp, err := o.client.Responses.New(ctx, responsesParams)
	if err != nil {
		return nil, fmt.Errorf("OpenAI generation failed: %w", err)
	}

	var content strings.Builder
	var toolCalls []ToolCallResult
	var reasoning strings.Builder
	for _, item := range resp.Output {
		switch item := item.AsAny().(type) {
		case responses.ResponseOutputMessage:
			for _, contentPart := range item.Content {
				switch part := contentPart.AsAny().(type) {
				case responses.ResponseOutputText:
					content.WriteString(part.Text)
				}
			}
		case responses.ResponseReasoningItem:
			for _, summary := range item.Summary {
				if summary.Text != "" {
					reasoning.WriteString(summary.Text)
				}
			}
		case responses.ResponseFunctionToolCall:
			toolCalls = append(toolCalls, ToolCallResult{
				ID:        item.ID,
				Name:      item.Name,
				Arguments: item.Arguments,
			})
		}
	}

	if content.Len() == 0 && reasoning.Len() > 0 {
		content = reasoning
	}

	finishReason := "stop"
	if resp.Status != "completed" {
		finishReason = string(resp.Status)
	}

	return &GenerateResponse{
		Content:      content.String(),
		FinishReason: finishReason,
		ResponseID:   resp.ID,
		ToolCalls:    toolCalls,
		Usage: UsageInfo{
			PromptTokens:     int(resp.Usage.InputTokens),
			CompletionTokens: int(resp.Usage.OutputTokens),
			TotalTokens:      int(resp.Usage.TotalTokens),
			ReasoningTokens:  int(resp.Usage.OutputTokensDetails.ReasoningTokens),
		},
	}, nil
}
