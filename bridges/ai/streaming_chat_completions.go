package ai

import (
	"context"
	"errors"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared/constant"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

type chatCompletionsTurnAdapter struct {
	streamingAdapterBase
}

func (a *chatCompletionsTurnAdapter) TrackRoomRunStreaming() bool {
	return false
}

func (a *chatCompletionsTurnAdapter) RunRound(
	ctx context.Context,
	evt *event.Event,
	round int,
) (bool, *ContextLengthError, error) {
	oc := a.oc
	log := a.log
	portal := a.portal
	meta := a.meta
	state := a.state
	typingSignals := a.typingSignals
	touchTyping := a.touchTyping
	isHeartbeat := a.isHeartbeat
	currentMessages := a.messages

	params := openai.ChatCompletionNewParams{
		Model:    oc.effectiveModelForAPI(meta),
		Messages: currentMessages,
	}
	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
		IncludeUsage: param.NewOpt(true),
	}
	if maxTokens := oc.effectiveMaxTokens(meta); maxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(maxTokens))
	}
	if temp := oc.effectiveTemperature(meta); temp > 0 {
		params.Temperature = openai.Float(temp)
	}
	params.Tools = oc.selectedChatStreamingTools(ctx, meta)

	stream := oc.api.Chat.Completions.NewStreaming(ctx, params)
	if stream == nil {
		initErr := errors.New("chat completions streaming not available")
		logChatCompletionsFailure(log, initErr, params, meta, currentMessages, "stream_init")
		return false, nil, oc.finishStreamingWithFailure(ctx, log, portal, state, meta, "error", initErr)
	}

	activeTools := newStreamToolRegistry()
	actions := newStreamTurnActions(
		ctx,
		oc,
		log,
		portal,
		state,
		meta,
		activeTools,
		typingSignals,
		touchTyping,
		isHeartbeat,
		round > 0,
		false,
	)
	var roundContent strings.Builder
	state.finishReason = ""

	_, cle, err := runStreamingStep(ctx, oc, portal, state, evt, stream,
		func(openai.ChatCompletionChunk) bool { return true },
		func(chunk openai.ChatCompletionChunk) (bool, *ContextLengthError, error) {
			if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				actions.updateUsage(
					chunk.Usage.PromptTokens,
					chunk.Usage.CompletionTokens,
					chunk.Usage.CompletionTokensDetails.ReasoningTokens,
					chunk.Usage.TotalTokens,
				)
			}

			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					roundDelta, err := actions.textDelta(choice.Delta.Content)
					if err != nil {
						return false, nil, &PreDeltaError{Err: err}
					}
					if roundDelta != "" {
						roundContent.WriteString(roundDelta)
					}
				}

				if choice.Delta.Refusal != "" {
					state.accumulated.WriteString(choice.Delta.Refusal)
					roundContent.WriteString(choice.Delta.Refusal)
					actions.refusalDelta(choice.Delta.Refusal)
					if err := state.turn.Err(); err != nil {
						return false, nil, &PreDeltaError{Err: err}
					}
				}

				for _, toolDelta := range choice.Delta.ToolCalls {
					actions.chatToolInputDelta(toolDelta)
				}

				if choice.FinishReason != "" {
					state.finishReason = string(choice.FinishReason)
				}
			}
			return false, nil, nil
		}, func(stepErr error) (*ContextLengthError, error) {
			if errors.Is(stepErr, context.Canceled) {
				return nil, oc.finishStreamingWithFailure(ctx, log, portal, state, meta, "cancelled", stepErr)
			}
			if cle := ParseContextLengthError(stepErr); cle != nil {
				return cle, nil
			}
			logChatCompletionsFailure(log, stepErr, params, meta, currentMessages, "stream_err")
			return nil, oc.finishStreamingWithFailure(ctx, log, portal, state, meta, "error", stepErr)
		})
	if cle != nil || err != nil {
		return false, cle, err
	}

	keys := activeTools.SortedKeys()
	toolCallParams := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(keys))

	if len(keys) > 0 {
		for _, key := range keys {
			tool := activeTools.Lookup(key)
			if tool == nil {
				continue
			}
			if tool.callID == "" {
				tool.callID = NewCallID()
			}
			toolName := strings.TrimSpace(tool.toolName)
			if toolName == "" {
				toolName = "unknown_tool"
			}
			tool.toolName = toolName

			argsJSON := normalizeToolArgsJSON(tool.input.String())
			toolCallParams = append(toolCallParams, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: tool.callID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      toolName,
						Arguments: argsJSON,
					},
					Type: constant.ValueOf[constant.Function](),
				},
			})

			actions.functionToolInputDone(tool.itemID, toolName, argsJSON)
		}
	}

	if shouldContinueChatToolLoop(state.finishReason, len(toolCallParams)) {
		state.needsTextSeparator = true
		assistantMsg := openai.ChatCompletionAssistantMessageParam{
			ToolCalls: toolCallParams,
		}
		if content := strings.TrimSpace(roundContent.String()); content != "" {
			assistantMsg.Content.OfString = param.NewOpt(content)
		}
		currentMessages = append(currentMessages, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistantMsg})
		for _, output := range state.pendingFunctionOutputs {
			currentMessages = append(currentMessages, openai.ToolMessage(output.output, output.callID))
		}
		if round >= maxStreamingToolRounds {
			log.Warn().Int("rounds", round+1).Msg("Max tool call rounds reached; stopping chat completions continuation")
			currentMessages = append(currentMessages, openai.AssistantMessage("Continuation stopped after reaching the maximum number of streaming tool rounds."))
			state.clearContinuationState()
			a.messages = currentMessages
			return false, nil, nil
		}
		if steerItems := oc.drainSteerQueue(state.roomID); len(steerItems) > 0 {
			for _, item := range steerItems {
				if item.pending.Type != pendingTypeText {
					log.Debug().
						Str("pending_type", string(item.pending.Type)).
						Str("message_id", strings.TrimSpace(item.messageID)).
						Msg("Skipping non-text steer queue item in chat completions continuation")
					continue
				}
				prompt := strings.TrimSpace(item.prompt)
				if prompt == "" {
					prompt = item.pending.MessageBody
				}
				prompt = strings.TrimSpace(prompt)
				if prompt == "" {
					continue
				}
				currentMessages = append(currentMessages, openai.UserMessage(prompt))
			}
		}
		// Chat Completions does not support MCP approvals; clearContinuationState
		// is safe here — it resets pendingFunctionOutputs (consumed above) and
		// pendingMcpApprovals (always empty for Chat).
		state.clearContinuationState()
		a.messages = currentMessages
		return true, nil, nil
	}

	a.messages = currentMessages
	return false, nil, nil
}

func (a *chatCompletionsTurnAdapter) Finalize(ctx context.Context) {
	oc := a.oc
	state := a.state
	portal := a.portal
	meta := a.meta

	oc.completeStreamingSuccess(ctx, a.log, portal, state, meta)

	a.log.Info().
		Str("turn_id", state.turn.ID()).
		Str("finish_reason", state.finishReason).
		Int("content_length", state.accumulated.Len()).
		Int("tool_calls", len(state.toolCalls)).
		Msg("Chat Completions streaming finished")

}

func (oc *AIClient) streamChatCompletions(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	messages []openai.ChatCompletionMessageParamUnion,
) (bool, *ContextLengthError, error) {
	portalID := ""
	if portal != nil {
		portalID = string(portal.ID)
	}
	log := zerolog.Ctx(ctx).With().
		Str("action", "stream_chat_completions").
		Str("portal", portalID).
		Logger()

	return oc.runStreamingTurn(ctx, log, evt, portal, meta, messages, func(prep streamingRunPrep, pruned []openai.ChatCompletionMessageParamUnion) streamingTurnAdapter {
		return &chatCompletionsTurnAdapter{
			streamingAdapterBase: newStreamingAdapterBase(oc, log, portal, meta, prep, pruned),
		}
	})
}

// convertToResponsesInput converts Chat Completion messages to Responses API input items
// Supports native multimodal content: images (ResponseInputImageParam), files/PDFs (ResponseInputFileParam)
// Note: Audio is handled via Chat Completions API fallback (SDK v3.16.0 lacks Responses API audio union support)
