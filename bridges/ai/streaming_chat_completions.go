package ai

import (
	"context"
	"errors"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

type chatCompletionsTurnAdapter struct {
	agentLoopProviderBase
}

func (a *chatCompletionsTurnAdapter) TrackRoomRunStreaming() bool {
	return false
}

func (a *chatCompletionsTurnAdapter) handleStreamStepError(
	ctx context.Context,
	params openai.ChatCompletionNewParams,
	currentMessages []openai.ChatCompletionMessageParamUnion,
	stepErr error,
) (*ContextLengthError, error) {
	if errors.Is(stepErr, context.Canceled) {
		return nil, a.oc.finishStreamingWithFailure(ctx, a.log, a.portal, a.state, a.meta, "cancelled", stepErr)
	}
	if cle := ParseContextLengthError(stepErr); cle != nil {
		return cle, a.oc.finishStreamingWithFailure(ctx, a.log, a.portal, a.state, a.meta, "context-length", stepErr)
	}
	logChatCompletionsFailure(a.log, stepErr, params, a.meta, currentMessages, "stream_err")
	return nil, a.oc.finishStreamingWithFailure(ctx, a.log, a.portal, a.state, a.meta, "error", stepErr)
}

func (a *chatCompletionsTurnAdapter) RunAgentTurn(
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

	params := oc.buildChatCompletionsAgentLoopParams(ctx, meta, currentMessages)

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

	_, cle, err := runAgentLoopStreamStep(ctx, oc, portal, state, evt, stream,
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
			return a.handleStreamStepError(ctx, params, currentMessages, stepErr)
		})
	if cle != nil || err != nil {
		return false, cle, err
	}

	toolCallParams, steeringPrompts := executeChatToolCallsSequentially(
		activeTools.SortedKeys(),
		activeTools,
		func(tool *activeToolCall, toolName, argsJSON string) {
			actions.functionToolInputDone(tool.itemID, toolName, argsJSON)
		},
		func() []string {
			return oc.getSteeringMessages(state.roomID)
		},
	)

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
		currentMessages = append(currentMessages, buildSteeringUserMessages(steeringPrompts)...)
		if round >= maxAgentLoopToolTurns {
			log.Warn().Int("rounds", round+1).Msg("Max tool call rounds reached; stopping chat completions continuation")
			currentMessages = append(currentMessages, openai.AssistantMessage("Continuation stopped after reaching the maximum number of streaming tool rounds."))
			state.clearContinuationState()
			a.messages = currentMessages
			return false, nil, nil
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

func (a *chatCompletionsTurnAdapter) FinalizeAgentLoop(ctx context.Context) {
	oc := a.oc
	state := a.state
	portal := a.portal
	meta := a.meta
	if state == nil || state.completedAtMs != 0 {
		return
	}

	oc.completeStreamingSuccess(ctx, a.log, portal, state, meta)

	a.log.Info().
		Str("turn_id", state.turn.ID()).
		Str("finish_reason", state.finishReason).
		Int("content_length", state.accumulated.Len()).
		Int("tool_calls", len(state.toolCalls)).
		Msg("Chat Completions streaming finished")

}

func (oc *AIClient) runChatCompletionsAgentLoop(
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

	return oc.runAgentLoop(ctx, log, evt, portal, meta, messages, func(prep streamingRunPrep, pruned []openai.ChatCompletionMessageParamUnion) agentLoopProvider {
		return &chatCompletionsTurnAdapter{
			agentLoopProviderBase: newAgentLoopProviderBase(oc, log, portal, meta, prep, pruned),
		}
	})
}

// convertToResponsesInput converts Chat Completion messages to Responses API input items
// Supports native multimodal content: images (ResponseInputImageParam), files/PDFs (ResponseInputFileParam)
// Note: Audio is handled via Chat Completions API fallback (SDK v3.16.0 lacks Responses API audio union support)
