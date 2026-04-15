package sdk

import (
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
