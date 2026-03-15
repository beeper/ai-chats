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
	oc.finalizeStreamingReplyAccumulator(state)
	oc.persistTerminalAssistantTurn(ctx, log, portal, state, meta)
	state.writer().MessageMetadata(ctx, oc.buildUIMessageMetadata(state, meta, true))
	state.turn.End(msgconv.MapFinishReason(state.finishReason))
	oc.noteStreamingPersistenceSideEffects(ctx, portal, state, meta)
	oc.maybeGenerateTitle(ctx, portal, finalRenderedBodyFallback(state))
	oc.recordProviderSuccess(ctx)
}
