package ai

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/bridges/ai/msgconv"
)

func (oc *AIClient) completeStreamingSuccess(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
) {
	state.completedAtMs = time.Now().UnixMilli()
	if state.finishReason == "" {
		state.finishReason = "stop"
	}
	if state.responseStatus == "" && state.responseID != "" {
		state.responseStatus = canonicalResponseStatus(state)
	}
	_ = log
	oc.finalizeStreamingReplyAccumulator(state)
	oc.persistTerminalAssistantTurn(ctx, portal, state, meta)
	if writer := state.writer(); writer != nil {
		writer.MessageMetadata(ctx, oc.buildUIMessageMetadata(state, meta, true))
	}
	if state != nil && state.turn != nil {
		state.turn.End(msgconv.MapFinishReason(state.finishReason))
	}
	oc.noteStreamingPersistenceSideEffects(ctx, portal, state, meta)
	oc.maybeGenerateTitle(ctx, portal, finalRenderedBodyFallback(state))
	oc.recordProviderSuccess(ctx)
}
