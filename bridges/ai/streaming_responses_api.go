package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// responseStreamContext holds loop-invariant parameters for processing a Responses API
// stream.  Only streamEvent and isContinuation change per event.
type responseStreamContext struct {
	base  *agentLoopProviderBase
	tools *streamToolRegistry
}

type responsesTurnAdapter struct {
	agentLoopProviderBase
	params      responses.ResponseNewParams
	initialized bool
	rsc         *responseStreamContext
}

func (a *responsesTurnAdapter) TrackRoomRunStreaming() bool {
	return true
}

func (a *responsesTurnAdapter) startInitialRound(ctx context.Context) (*ssestream.Stream[responses.ResponseStreamEventUnion], error) {
	if !a.initialized {
		input := promptContextToResponsesInput(a.prompt)
		a.params = a.oc.buildResponsesAgentLoopParams(ctx, a.meta, a.prompt.SystemPrompt, input, false)
		if len(a.params.Tools) > 0 {
			zerolog.Ctx(ctx).Debug().Int("count", len(a.params.Tools)).Msg("Added streaming turn tools")
		}
		if a.oc.isOpenRouterProvider() {
			ctx = WithPDFEngine(ctx, a.oc.effectivePDFEngine(a.meta))
		}
		a.initialized = true
	}
	stream := a.oc.api.Responses.NewStreaming(ctx, a.params)
	if stream == nil {
		return nil, errors.New("responses streaming not available")
	}
	return stream, nil
}

func (a *responsesTurnAdapter) startContinuationRound(ctx context.Context) (*ssestream.Stream[responses.ResponseStreamEventUnion], responses.ResponseNewParams, error) {
	state := a.state
	if ctx.Err() != nil {
		if state.hasInitialMessageTarget() && state.accumulated.Len() > 0 {
			a.oc.flushPartialStreamingMessage(context.Background(), a.portal, state, a.meta)
		}
		return nil, responses.ResponseNewParams{}, ctx.Err()
	}
	pendingOutputs := append([]functionCallOutput(nil), state.pendingFunctionOutputs...)
	continuationParams := a.oc.buildContinuationParams(ctx, &a.prompt, state, a.meta, pendingOutputs)

	state.needsTextSeparator = true
	stream := a.oc.api.Responses.NewStreaming(ctx, continuationParams)
	if stream == nil {
		return nil, continuationParams, errors.New("continuation streaming not available")
	}
	state.clearContinuationState()
	return stream, continuationParams, nil
}

func (a *responsesTurnAdapter) RunAgentTurn(
	ctx context.Context,
	evt *event.Event,
	round int,
) (bool, *ContextLengthError, error) {
	state := a.state
	var (
		stream *ssestream.Stream[responses.ResponseStreamEventUnion]
		params responses.ResponseNewParams
		err    error
	)

	if round == 0 {
		stream, err = a.startInitialRound(ctx)
		params = a.params
		if err != nil {
			logResponsesFailure(a.log, err, params, a.meta, a.prompt, "stream_init")
			return false, nil, &PreDeltaError{Err: err}
		}
	} else {
		if len(state.pendingFunctionOutputs) == 0 && len(state.pendingSteeringPrompts) == 0 {
			return false, nil, nil
		}
		if round > maxAgentLoopToolTurns {
			err = fmt.Errorf("max responses tool call rounds reached (%d)", maxAgentLoopToolTurns)
			a.log.Warn().Err(err).Int("pending_outputs", len(state.pendingFunctionOutputs)).Msg("Stopping responses continuation loop")
			return false, nil, a.oc.finalizeStreamingTurn(ctx, a.portal, state, a.meta, streamingFinalizeParams{
				reason: "error",
				err:    err,
			})
		}
		a.log.Debug().
			Int("pending_outputs", len(state.pendingFunctionOutputs)).
			Int("prompt_messages", len(a.prompt.Messages)).
			Msg("Continuing stateless response with pending tool actions")
		stream, params, err = a.startContinuationRound(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return false, nil, a.oc.finalizeStreamingTurn(ctx, a.portal, state, a.meta, streamingFinalizeParams{
					reason: "cancelled",
					err:    err,
				})
			}
			logResponsesFailure(a.log, err, params, a.meta, a.prompt, "continuation_init")
			return false, nil, a.oc.finalizeStreamingTurn(ctx, a.portal, state, a.meta, streamingFinalizeParams{
				reason: "error",
				err:    err,
			})
		}
	}

	tools := newStreamToolRegistry()
	a.rsc.tools = tools
	accepted := round > 0
	done, cle, err := runAgentLoopStreamStep(ctx, state, stream,
		func(streamEvent responses.ResponseStreamEventUnion) (bool, *ContextLengthError, error) {
			if !accepted {
				a.oc.markTurnAccepted(ctx, a.portal, state, a.meta)
				accepted = true
			}
			done, cle, evtErr := a.oc.processResponseStreamEvent(ctx, a.rsc, streamEvent, round > 0)
			if done && evtErr != nil {
				stage := "stream_event_error"
				if round > 0 {
					stage = "continuation_event_error"
				}
				logResponsesFailure(a.log, evtErr, params, a.meta, a.prompt, stage)
			}
			return done, cle, evtErr
		},
		func(stepErr error) (*ContextLengthError, error) {
			stage := "stream_err"
			if round > 0 {
				stage = "continuation_err"
			}
			return a.oc.finalizeStreamingStepError(ctx, a.portal, state, a.meta, round == 0, context.Background(), stepErr, func(err error) {
				logResponsesFailure(a.log, err, params, a.meta, a.prompt, stage)
			})
		},
	)
	if cle != nil || err != nil {
		return false, cle, err
	}
	if done {
		return state != nil && (len(state.pendingFunctionOutputs) > 0 || len(state.pendingSteeringPrompts) > 0), nil, nil
	}

	return state != nil && (len(state.pendingFunctionOutputs) > 0 || len(state.pendingSteeringPrompts) > 0), nil, nil
}

func (a *responsesTurnAdapter) FinalizeAgentLoop(ctx context.Context) {
	if a.state == nil || a.state.isFinalized() {
		return
	}
	_ = a.oc.finalizeStreamingTurn(ctx, a.portal, a.state, a.meta, streamingFinalizeParams{
		success:               true,
		finalizeAccumulator:   true,
		recordProviderSuccess: true,
		generateTitle:         true,
	})

	a.log.Info().
		Str("turn_id", a.state.turn.ID()).
		Str("finish_reason", a.state.finishReason).
		Int("content_length", a.state.accumulated.Len()).
		Int("reasoning_length", a.state.reasoning.Len()).
		Int("tool_calls", len(a.state.toolCalls)).
		Str("response_id", a.state.responseID).
		Msg("Responses API streaming finished")
}

// processResponseStreamEvent handles a single Responses API stream event.
// Returns done=true when the caller's loop should break (error/fatal), along with
// any context-length error or general error.  The caller is responsible for
// calling logResponsesFailure when err != nil.
func (oc *AIClient) processResponseStreamEvent(
	ctx context.Context,
	rsc *responseStreamContext,
	streamEvent responses.ResponseStreamEventUnion,
	isContinuation bool,
) (done bool, cle *ContextLengthError, err error) {
	log := rsc.base.log
	portal := rsc.base.portal
	state := rsc.base.state
	meta := rsc.base.meta
	tools := rsc.tools
	contSuffix := ""
	if isContinuation {
		contSuffix = " (continuation)"
	}
	actions := newStreamTurnActions(
		ctx,
		oc,
		log,
		portal,
		state,
		meta,
		tools,
		rsc.base.typingSignals,
		rsc.base.touchTyping,
		rsc.base.isHeartbeat,
		isContinuation,
		!isContinuation,
	)
	applyResponseLifecycle := func(eventType string, response responses.Response) {
		if !state.applyResponseLifecycleEvent(eventType, response) {
			return
		}

		base := oc.buildUIMessageMetadata(state, meta, false)
		extra := responseMetadataDeltaFromResponse(response)
		if len(extra) > 0 {
			base = mergeMaps(base, extra)
		}
		state.writer().MessageMetadata(ctx, base)

		if eventType == "response.failed" {
			if msg := strings.TrimSpace(response.Error.Message); msg != "" {
				state.writer().Error(ctx, msg)
			}
		}
	}

	switch streamEvent.Type {
	case "response.created", "response.queued", "response.in_progress":
		applyResponseLifecycle(streamEvent.Type, streamEvent.Response)

	case "response.failed":
		applyResponseLifecycle(streamEvent.Type, streamEvent.Response)
		errText := strings.TrimSpace(streamEvent.Response.Error.Message)
		if errText == "" {
			errText = "response failed"
		}
		return true, nil, oc.finalizeStreamingTurn(ctx, portal, state, meta, streamingFinalizeParams{
			reason: "error",
			err:    errors.New(errText),
		})

	case "response.incomplete":
		applyResponseLifecycle(streamEvent.Type, streamEvent.Response)
		actions.finalizeMetadata()
		log.Debug().
			Str("reason", state.finishReason).
			Str("response_id", state.responseID).
			Str("response_status", state.responseStatus).
			Msg("Response stream ended incomplete" + contSuffix)
		return true, nil, nil

	case "response.output_item.added":
		actions.outputItemAdded(streamEvent.Item)

	case "response.output_item.done":
		actions.outputItemDone(streamEvent.Item)

	case "response.output_text.delta":
		if _, err := actions.textDelta(streamEvent.Delta); err != nil {
			return true, nil, &PreDeltaError{Err: err}
		}

	case "response.reasoning_text.delta":
		if err := actions.reasoningDelta(streamEvent.Delta); err != nil {
			return true, nil, &PreDeltaError{Err: err}
		}

	case "response.reasoning_summary_text.delta":
		actions.reasoningText(streamEvent.Delta)

	case "response.reasoning_text.done", "response.reasoning_summary_text.done":
		actions.reasoningText(streamEvent.Text)

	case "response.refusal.delta":
		actions.refusalDelta(streamEvent.Delta)

	case "response.refusal.done":
		actions.refusalDone(streamEvent.Refusal)

	case "response.output_text.done":
		// text-end is emitted from emitUIFinish to keep one contiguous part.

	case "response.function_call_arguments.delta":
		actions.functionToolInputDelta(streamEvent.ItemID, streamEvent.Name, streamEvent.Delta)

	case "response.function_call_arguments.done":
		actions.functionToolInputDone(streamEvent.ItemID, streamEvent.Name, streamEvent.Arguments)
		if steeringPrompts := oc.getSteeringMessages(state.roomID); len(steeringPrompts) > 0 {
			state.addPendingSteeringPrompts(steeringPrompts)
			return true, nil, nil
		}

	case "response.output_text.annotation.added":
		actions.annotationAdded(streamEvent.Annotation, streamEvent.AnnotationIndex)

	case "response.completed":
		applyResponseLifecycle(streamEvent.Type, streamEvent.Response)
		if streamEvent.Response.Usage.TotalTokens > 0 || streamEvent.Response.Usage.InputTokens > 0 || streamEvent.Response.Usage.OutputTokens > 0 {
			actions.updateUsage(
				streamEvent.Response.Usage.InputTokens,
				streamEvent.Response.Usage.OutputTokens,
				streamEvent.Response.Usage.OutputTokensDetails.ReasoningTokens,
				streamEvent.Response.Usage.TotalTokens,
			)
		}
		actions.finalizeMetadata()

		log.Debug().Str("reason", state.finishReason).Str("response_id", state.responseID).
			Msg("Response stream completed" + contSuffix)
		return true, nil, nil

	case "error":
		apiErr := fmt.Errorf("API error: %s", streamEvent.Message)
		// Check for context length error (only on initial stream, not continuation)
		if !isContinuation {
			if strings.Contains(streamEvent.Message, "context_length") || strings.Contains(streamEvent.Message, "token") {
				return true, &ContextLengthError{
					OriginalError: fmt.Errorf("%s", streamEvent.Message),
				}, nil
			}
		}
		return true, nil, oc.finalizeStreamingTurn(ctx, portal, state, meta, streamingFinalizeParams{
			reason: "error",
			err:    apiErr,
		})

	default:
		// Ignore unknown events
	}

	return false, nil, nil
}

// runResponsesAgentLoop handles the Responses API provider adapter under the canonical agent loop.
func (oc *AIClient) runResponsesAgentLoopPrompt(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt PromptContext,
) (bool, *ContextLengthError, error) {
	portalID := ""
	if portal != nil {
		portalID = string(portal.ID)
	}
	log := zerolog.Ctx(ctx).With().
		Str("portal_id", portalID).
		Logger()
	return oc.runAgentLoop(ctx, log, evt, portal, meta, prompt, func(prep streamingRunPrep, prompt PromptContext) agentLoopProvider {
		base := newAgentLoopProviderBase(oc, log, portal, meta, prep, prompt)
		return &responsesTurnAdapter{
			agentLoopProviderBase: base,
			rsc: &responseStreamContext{
				base:  &base,
				tools: newStreamToolRegistry(),
			},
		}
	})
}
