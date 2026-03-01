package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func (oc *AIClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) error {
	if oc == nil || state == nil || portal == nil {
		return nil
	}
	streamtransport.SendDebouncedEdit(ctx, streamtransport.DebouncedEditParams{
		Portal:         portal,
		Force:          force,
		SuppressSend:   state.suppressSend,
		VisibleBody:    state.visibleAccumulated.String(),
		FallbackBody:   state.accumulated.String(),
		InitialEventID: state.initialEventID,
		TurnID:  state.turnID,
		Intent:  oc.getModelIntent(ctx, portal),
		Log:      oc.loggerForContext(ctx),
	})
	return nil
}
