package bridgeadapter

import (
	"time"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

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
		PreBuilt: streamtransport.BuildConvertedEdit(&event.MessageEventContent{
			MsgType:       event.MsgText,
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
