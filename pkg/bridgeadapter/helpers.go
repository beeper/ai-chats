package bridgeadapter

import (
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
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
