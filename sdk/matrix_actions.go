package sdk

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func SendMessageStatus(
	ctx context.Context,
	portal *bridgev2.Portal,
	roomID id.RoomID,
	sourceEventID id.EventID,
	status event.MessageStatus,
	message string,
) {
	if portal == nil || portal.Bridge == nil || portal.Bridge.Matrix == nil || sourceEventID == "" {
		return
	}
	statusContent := bridgev2.MessageStatus{
		Status:    status,
		Message:   message,
		IsCertain: true,
	}
	portal.Bridge.Matrix.SendMessageStatus(ctx, &statusContent, &bridgev2.MessageStatusEventInfo{
		RoomID:        roomID,
		SourceEventID: sourceEventID,
	})
}
