package sdk

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

// findPortalMessageByID performs a strict lookup by network message ID and
// part ID within the current portal.
func findPortalMessageByID(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	networkMessageID networkid.MessageID,
	partID networkid.PartID,
) (*database.Message, error) {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil || login.Bridge.DB.Message == nil || portal == nil || networkMessageID == "" || partID == "" {
		return nil, nil
	}
	parts, err := login.Bridge.DB.Message.GetAllPartsByID(ctx, portal.PortalKey.Receiver, networkMessageID)
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		if part != nil && part.Room == portal.PortalKey && part.PartID == partID {
			return part, nil
		}
	}
	return nil, nil
}

func findPortalMessageByMXID(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	initialEventID id.EventID,
) (*database.Message, error) {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil || login.Bridge.DB.Message == nil || portal == nil || initialEventID == "" {
		return nil, nil
	}
	msg, err := login.Bridge.DB.Message.GetPartByMXID(ctx, initialEventID)
	if err != nil {
		return nil, err
	}
	if msg == nil || msg.Room != portal.PortalKey {
		return nil, nil
	}
	return msg, nil
}
