package sdk

import (
	"context"
	"time"

	"github.com/rs/zerolog"
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

// UpsertAssistantMessageParams holds parameters for UpsertAssistantMessage.
type UpsertAssistantMessageParams struct {
	Login            *bridgev2.UserLogin
	Portal           *bridgev2.Portal
	SenderID         networkid.UserID
	NetworkMessageID networkid.MessageID
	InitialEventID   id.EventID
	Metadata         any // must satisfy database.MetaMerger
	Logger           zerolog.Logger
}

// UpsertAssistantMessage updates an existing message's metadata or inserts a new one.
// The canonical row is keyed by NetworkMessageID; InitialEventID is only stored as MXID.
func UpsertAssistantMessage(ctx context.Context, p UpsertAssistantMessageParams) {
	if p.Login == nil || p.Portal == nil || p.NetworkMessageID == "" || p.InitialEventID == "" {
		return
	}
	db := p.Login.Bridge.DB.Message

	existing, err := findPortalMessageByID(ctx, p.Login, p.Portal, p.NetworkMessageID, networkid.PartID("0"))
	if err != nil {
		p.Logger.Warn().Err(err).Str("msg_id", string(p.NetworkMessageID)).Msg("Failed to look up assistant message metadata")
		return
	}
	if existing != nil {
		existing.Metadata = p.Metadata
		if err := db.Update(ctx, existing); err != nil {
			p.Logger.Warn().Err(err).Str("msg_id", string(existing.ID)).Msg("Failed to update assistant message metadata")
		} else {
			p.Logger.Debug().Str("msg_id", string(existing.ID)).Msg("Updated assistant message metadata")
		}
		return
	}

	assistantMsg := &database.Message{
		ID:        p.NetworkMessageID,
		PartID:    networkid.PartID("0"),
		Room:      p.Portal.PortalKey,
		SenderID:  p.SenderID,
		MXID:      p.InitialEventID,
		Timestamp: time.Now(),
		Metadata:  p.Metadata,
	}
	if err := db.Insert(ctx, assistantMsg); err != nil {
		p.Logger.Warn().Err(err).Msg("Failed to insert assistant message to database")
	} else {
		p.Logger.Debug().Str("msg_id", string(assistantMsg.ID)).Msg("Inserted assistant message to database")
	}
}
