package ai

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/sdk"
)

func (oc *AIClient) sendSuccessMessageStatus(ctx context.Context, portal *bridgev2.Portal, events []*event.Event) {
	if portal == nil || portal.Bridge == nil {
		return
	}
	msgStatus := bridgev2.MessageStatus{
		Status:    event.MessageStatusSuccess,
		IsCertain: true,
	}
	for _, statusEvt := range events {
		if statusEvt == nil {
			continue
		}
		if info := sdk.StatusEventInfoFromPortalEvent(portal, statusEvt); info != nil {
			portal.Bridge.Matrix.SendMessageStatus(ctx, &msgStatus, info)
		}
	}
}

func (oc *AIClient) acceptPendingMessages(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if oc == nil || portal == nil || portal.MXID == "" || state == nil || state.suppressSend {
		return
	}

	messages := oc.consumeRoomRunAcceptedMessages(state.roomID)
	statusEvents := oc.consumeRoomRunStatusEvents(state.roomID)
	if len(messages) == 0 && len(statusEvents) == 0 {
		return
	}

	for _, msg := range messages {
		if msg == nil {
			continue
		}
		oc.persistAcceptedUserMessage(ctx, portal, msg)
	}
	oc.sendSuccessMessageStatus(ctx, portal, statusEvents)
}
