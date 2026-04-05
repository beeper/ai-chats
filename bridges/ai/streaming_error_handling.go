package ai

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/ai/msgconv"
)

// NonFallbackError marks an error as ineligible for fallback retries once output has been sent.
type NonFallbackError struct {
	Err error
}

func (e *NonFallbackError) Error() string {
	return e.Err.Error()
}

func (e *NonFallbackError) Unwrap() error {
	return e.Err
}

func streamFailureError(state *streamingState, err error) error {
	if state != nil && state.hasInitialMessageTarget() {
		return &NonFallbackError{Err: err}
	}
	return &PreDeltaError{Err: err}
}

func (oc *AIClient) finishStreamingWithFailure(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	reason string,
	err error,
) error {
	if state != nil && state.stop.Load() != nil && reason == "cancelled" {
		reason = "stop"
	}
	state.finishReason = reason
	state.completedAtMs = time.Now().UnixMilli()
	_ = log
	oc.persistTerminalAssistantTurn(ctx, portal, state, meta)
	if writer := state.writer(); writer != nil {
		writer.MessageMetadata(ctx, oc.buildUIMessageMetadata(state, meta, true))
	}
	switch reason {
	case "cancelled":
		state.writer().Abort(ctx, "cancelled")
		if state.turn != nil {
			state.turn.End("cancelled")
		}
	case "stop":
		if state.turn != nil {
			state.turn.End(msgconv.MapFinishReason(reason))
		}
	default:
		if state.turn != nil {
			state.turn.EndWithError(err.Error())
		}
	}
	oc.noteStreamingPersistenceSideEffects(ctx, portal, state, meta)
	return streamFailureError(state, err)
}

func (oc *AIClient) handleResponsesStreamErr(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	err error,
	includeContextLength bool,
) (*ContextLengthError, error) {
	if errors.Is(err, context.Canceled) {
		return nil, oc.finishStreamingWithFailure(context.Background(), *oc.loggerForContext(ctx), portal, state, meta, "cancelled", err)
	}

	if includeContextLength {
		cle := ParseContextLengthError(err)
		if cle != nil {
			return cle, nil
		}
	}

	return nil, oc.finishStreamingWithFailure(ctx, *oc.loggerForContext(ctx), portal, state, meta, "error", err)
}
