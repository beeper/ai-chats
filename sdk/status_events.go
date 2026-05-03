package sdk

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// StatusEventInfoFromPortalEvent canonicalizes bridgev2 status metadata using the
// resolved portal when inbound Matrix events omit their room ID.
func StatusEventInfoFromPortalEvent(portal *bridgev2.Portal, evt *event.Event) *bridgev2.MessageStatusEventInfo {
	if evt == nil {
		return nil
	}
	info := bridgev2.StatusEventInfoFromEvent(evt)
	if info == nil {
		return nil
	}
	if info.RoomID == "" && portal != nil && portal.MXID != "" {
		info.RoomID = portal.MXID
	}
	return info
}

// SendMessageStatus is the single boundary for Matrix message status emission.
// Bridge code should use this helper instead of reaching through Bridge.Matrix.
func SendMessageStatus(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, status bridgev2.MessageStatus) {
	if portal == nil || portal.Bridge == nil || portal.Bridge.Matrix == nil {
		return
	}
	info := StatusEventInfoFromPortalEvent(portal, evt)
	if info == nil {
		return
	}
	portal.Bridge.Matrix.SendMessageStatus(ctx, &status, info)
}
