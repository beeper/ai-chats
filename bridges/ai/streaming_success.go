package ai

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
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
	oc.emitUIFinish(ctx, portal, state, meta)
	oc.persistTerminalAssistantTurn(ctx, log, portal, state, meta)
	oc.maybeGenerateTitle(ctx, portal, state.accumulated.String())
	oc.recordProviderSuccess(ctx)
}
