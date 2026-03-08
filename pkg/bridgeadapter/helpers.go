package bridgeadapter

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

func BuildMetaTypes(portal, message, userLogin, ghost func() any) database.MetaTypes {
	return database.MetaTypes{
		Portal:    portal,
		Message:   message,
		UserLogin: userLogin,
		Ghost:     ghost,
	}
}

// BuildSystemNotice creates a ConvertedMessage containing a single MsgNotice part.
func BuildSystemNotice(body string) *bridgev2.ConvertedMessage {
	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:   networkid.PartID("0"),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType:  event.MsgNotice,
				Body:     body,
				Mentions: &event.Mentions{},
			},
		}},
	}
}

// SendDebouncedStreamEditParams holds the parameters for SendDebouncedStreamEdit.
type SendDebouncedStreamEditParams struct {
	Login            *bridgev2.UserLogin
	Portal           *bridgev2.Portal
	Sender           bridgev2.EventSender
	NetworkMessageID networkid.MessageID
	SuppressSend     bool
	VisibleBody      string
	FallbackBody     string
	LogKey           string
	Force            bool
}

// SendDebouncedStreamEdit builds and queues a debounced stream edit via the bridge pipeline.
func SendDebouncedStreamEdit(p SendDebouncedStreamEditParams) error {
	if p.Login == nil || p.Portal == nil {
		return nil
	}
	content := streamtransport.BuildDebouncedEditContent(streamtransport.DebouncedEditParams{
		PortalMXID:   p.Portal.MXID.String(),
		Force:        p.Force,
		SuppressSend: p.SuppressSend,
		VisibleBody:  p.VisibleBody,
		FallbackBody: p.FallbackBody,
	})
	if content == nil || p.NetworkMessageID == "" {
		return nil
	}
	p.Login.QueueRemoteEvent(&RemoteEdit{
		Portal:        p.Portal.PortalKey,
		Sender:        p.Sender,
		TargetMessage: p.NetworkMessageID,
		Timestamp:     time.Now(),
		LogKey:        p.LogKey,
		PreBuilt: streamtransport.BuildRenderedConvertedEdit(streamtransport.RenderedMarkdownContent{
			Body:          content.Body,
			Format:        content.Format,
			FormattedBody: content.FormattedBody,
		}, map[string]any{
			"com.beeper.dont_render_edited": true,
			"m.mentions":                    map[string]any{},
		}),
	})
	return nil
}

// DMChatInfoParams holds the parameters for BuildDMChatInfo.
type DMChatInfoParams struct {
	Title             string
	HumanUserID       networkid.UserID
	LoginID           networkid.UserLoginID
	BotUserID         networkid.UserID
	BotDisplayName    string
	CanBackfill       bool
	CapabilitiesEvent event.Type
	SettingsEvent     event.Type
}

// BuildDMChatInfo creates a ChatInfo for a DM room between a human user and a bot ghost.
func BuildDMChatInfo(p DMChatInfoParams) *bridgev2.ChatInfo {
	members := bridgev2.ChatMemberMap{
		p.HumanUserID: {
			EventSender: bridgev2.EventSender{
				IsFromMe:    true,
				SenderLogin: p.LoginID,
			},
			Membership: event.MembershipJoin,
		},
		p.BotUserID: {
			EventSender: bridgev2.EventSender{
				Sender:      p.BotUserID,
				SenderLogin: p.LoginID,
			},
			Membership: event.MembershipJoin,
			UserInfo: &bridgev2.UserInfo{
				Name:  ptr.Ptr(p.BotDisplayName),
				IsBot: ptr.Ptr(true),
			},
			MemberEventExtra: map[string]any{
				"displayname": p.BotDisplayName,
			},
		},
	}
	return &bridgev2.ChatInfo{
		Name:        ptr.Ptr(p.Title),
		Type:        ptr.Ptr(database.RoomTypeDM),
		CanBackfill: p.CanBackfill,
		Members: &bridgev2.ChatMemberList{
			IsFull:      true,
			OtherUserID: p.BotUserID,
			MemberMap:   members,
			PowerLevels: &bridgev2.PowerLevelOverrides{
				Events: map[event.Type]int{
					p.CapabilitiesEvent: 100,
					p.SettingsEvent:     0,
				},
			},
		},
	}
}

// SendViaPortalParams holds the parameters for SendViaPortal.
type SendViaPortalParams struct {
	Login     *bridgev2.UserLogin
	Portal    *bridgev2.Portal
	Sender    bridgev2.EventSender
	IDPrefix  string // e.g. "ai", "codex", "opencode"
	LogKey    string // zerolog field name, e.g. "ai_msg_id"
	MsgID     networkid.MessageID
	Converted *bridgev2.ConvertedMessage
}

// SendViaPortal sends a pre-built message through bridgev2's QueueRemoteEvent pipeline.
// If MsgID is empty, a new one is generated using IDPrefix.
func SendViaPortal(p SendViaPortalParams) (id.EventID, networkid.MessageID, error) {
	if p.Portal == nil || p.Portal.MXID == "" {
		return "", "", fmt.Errorf("invalid portal")
	}
	if p.Login == nil {
		return "", p.MsgID, fmt.Errorf("bridge unavailable")
	}
	if p.MsgID == "" {
		p.MsgID = NewMessageID(p.IDPrefix)
	}
	evt := &RemoteMessage{
		Portal:    p.Portal.PortalKey,
		ID:        p.MsgID,
		Sender:    p.Sender,
		Timestamp: time.Now(),
		LogKey:    p.LogKey,
		PreBuilt:  p.Converted,
	}
	result := p.Login.QueueRemoteEvent(evt)
	if !result.Success {
		if result.Error != nil {
			return "", p.MsgID, fmt.Errorf("send failed: %w", result.Error)
		}
		return "", p.MsgID, fmt.Errorf("send failed")
	}
	return result.EventID, p.MsgID, nil
}

func BuildChatInfoWithFallback(metaTitle, portalName, fallbackTitle, portalTopic string) *bridgev2.ChatInfo {
	title := metaTitle
	if title == "" {
		if portalName != "" {
			title = portalName
		} else {
			title = fallbackTitle
		}
	}
	return &bridgev2.ChatInfo{
		Name:  ptr.Ptr(title),
		Topic: ptr.NonZero(portalTopic),
	}
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
// If NetworkMessageID is set, tries to find and update the existing row first.
// Falls back to inserting a new row keyed by InitialEventID.
func UpsertAssistantMessage(ctx context.Context, p UpsertAssistantMessageParams) {
	if p.Login == nil || p.Portal == nil {
		return
	}
	db := p.Login.Bridge.DB.Message

	if p.NetworkMessageID != "" {
		receiver := p.Portal.Receiver
		if receiver == "" {
			receiver = p.Login.ID
		}
		var existing *database.Message
		var err error
		if receiver != "" {
			existing, err = db.GetPartByID(ctx, receiver, p.NetworkMessageID, networkid.PartID("0"))
		}
		if existing == nil && p.InitialEventID != "" {
			existing, err = db.GetPartByMXID(ctx, p.InitialEventID)
		}
		if err == nil && existing != nil {
			existing.Metadata = p.Metadata
			if err := db.Update(ctx, existing); err != nil {
				p.Logger.Warn().Err(err).Str("msg_id", string(existing.ID)).Msg("Failed to update assistant message metadata")
			} else {
				p.Logger.Debug().Str("msg_id", string(existing.ID)).Msg("Updated assistant message metadata")
			}
			return
		}
		p.Logger.Warn().
			Err(err).
			Stringer("mxid", p.InitialEventID).
			Str("msg_id", string(p.NetworkMessageID)).
			Msg("Could not find existing DB row for update, falling back to insert")
	}

	if p.InitialEventID == "" {
		return
	}
	assistantMsg := &database.Message{
		ID:        MatrixMessageID(p.InitialEventID),
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
