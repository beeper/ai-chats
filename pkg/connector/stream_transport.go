package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

func (oc *AIClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) error {
	if oc == nil || state == nil || portal == nil {
		return nil
	}
	return bridgeadapter.SendDebouncedStreamEdit(bridgeadapter.SendDebouncedStreamEditParams{
		Login:            oc.UserLogin,
		Portal:           portal,
		Sender:           oc.senderForPortal(ctx, portal),
		NetworkMessageID: state.networkMessageID,
		SuppressSend:     state.suppressSend,
		VisibleBody:      state.visibleAccumulated.String(),
		FallbackBody:     state.accumulated.String(),
		LogKey:           "ai_edit_target",
		Force:            force,
	})
}
