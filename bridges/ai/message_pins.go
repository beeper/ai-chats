package ai

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func getPinnedEventIDs(ctx context.Context, btc *BridgeToolContext) []string {
	var pinnedEvents []string
	if btc == nil || btc.Client == nil || btc.Client.UserLogin == nil || btc.Client.UserLogin.Bridge == nil || btc.Portal == nil {
		return pinnedEvents
	}
	matrixConn := btc.Client.UserLogin.Bridge.Matrix
	stateConn, ok := matrixConn.(bridgev2.MatrixConnectorWithArbitraryRoomState)
	if !ok {
		return pinnedEvents
	}
	stateEvent, err := stateConn.GetStateEvent(ctx, btc.Portal.MXID, event.StatePinnedEvents, "")
	if err == nil && stateEvent != nil {
		if content, ok := stateEvent.Content.Parsed.(*event.PinnedEventsEventContent); ok {
			for _, evtID := range content.Pinned {
				pinnedEvents = append(pinnedEvents, evtID.String())
			}
		}
	}
	return pinnedEvents
}
