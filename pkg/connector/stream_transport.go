package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func (oc *AIClient) sendDebouncedStreamEdit(ctx context.Context, portal *bridgev2.Portal, state *streamingState, force bool) error {
	if oc == nil || state == nil || portal == nil {
		return nil
	}
	intent, _ := oc.getIntentForPortal(ctx, portal, bridgev2.RemoteEventMessage)
	streamtransport.SendDebouncedEdit(ctx, streamtransport.DebouncedEditParams{
		Portal:         portal,
		Force:          force,
		SuppressSend:   state.suppressSend,
		VisibleBody:    state.visibleAccumulated.String(),
		FallbackBody:   state.accumulated.String(),
		InitialEventID: state.initialEventID,
		TurnID:         state.turnID,
		SendFunc:       intentSendFunc(intent),
		Log:            oc.loggerForContext(ctx),
	})
	return nil
}

// intentSendFunc wraps a MatrixAPI intent into a streamtransport.SendFunc.
func intentSendFunc(intent bridgev2.MatrixAPI) streamtransport.SendFunc {
	if intent == nil {
		return nil
	}
	return func(ctx context.Context, roomID id.RoomID, content *event.Content) error {
		_, err := intent.SendMessage(ctx, roomID, event.EventMessage, content, nil)
		return err
	}
}
