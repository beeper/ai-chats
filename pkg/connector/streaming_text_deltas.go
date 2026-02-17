package connector

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
)

func (oc *AIClient) ensureInitialStreamMessage(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	isHeartbeat bool,
	initialText string,
	errText string,
	logMessage string,
) error {
	if !state.firstToken {
		return nil
	}
	state.firstToken = false
	state.firstTokenAtMs = time.Now().UnixMilli()

	if !state.suppressSend && !isHeartbeat {
		oc.ensureGhostDisplayName(ctx, oc.effectiveModel(meta))
		state.initialEventID = oc.sendInitialStreamMessage(ctx, portal, initialText, state.turnID, state.replyTarget)
		if state.initialEventID == "" {
			log.Error().Msg(logMessage)
			state.finishReason = "error"
			oc.emitUIError(ctx, portal, state, errText)
			oc.emitUIFinish(ctx, portal, state, meta)
			return errors.New(errText)
		}
	}
	return nil
}

func (oc *AIClient) handleResponseOutputTextDelta(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	typingSignals *TypingSignaler,
	isHeartbeat bool,
	delta string,
	errText string,
	logMessage string,
) error {
	delta = maybePrependTextSeparator(state, delta)
	state.accumulated.WriteString(delta)

	var parsed *streamingDirectiveResult
	if state.replyAccumulator != nil {
		parsed = state.replyAccumulator.Consume(delta, false)
	}
	if parsed == nil {
		return nil
	}

	oc.applyStreamingReplyTarget(state, parsed)
	cleaned := parsed.Text
	if typingSignals != nil {
		typingSignals.SignalTextDelta(cleaned)
	}
	if cleaned == "" {
		return nil
	}

	state.visibleAccumulated.WriteString(cleaned)
	if state.firstToken && state.visibleAccumulated.Len() > 0 {
		if err := oc.ensureInitialStreamMessage(
			ctx,
			log,
			portal,
			state,
			meta,
			isHeartbeat,
			state.visibleAccumulated.String(),
			errText,
			logMessage,
		); err != nil {
			return err
		}
	}
	oc.emitUITextDelta(ctx, portal, state, cleaned)
	return nil
}

func (oc *AIClient) handleResponseReasoningTextDelta(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	isHeartbeat bool,
	delta string,
	errText string,
	logMessage string,
) error {
	state.reasoning.WriteString(delta)
	if state.firstToken && state.reasoning.Len() > 0 {
		if err := oc.ensureInitialStreamMessage(
			ctx,
			log,
			portal,
			state,
			meta,
			isHeartbeat,
			"...",
			errText,
			logMessage,
		); err != nil {
			return err
		}
	}
	oc.emitUIReasoningDelta(ctx, portal, state, delta)
	return nil
}
