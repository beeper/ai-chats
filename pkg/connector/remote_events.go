package connector

import (
	"time"

	"github.com/rs/zerolog"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/bridgeadapter"
	"github.com/beeper/agentremote/pkg/connector/msgconv"
)

// -----------------------------------------------------------------------
// AIRemoteMessageRemove — for redacting messages
// -----------------------------------------------------------------------

var _ bridgev2.RemoteMessageRemove = (*AIRemoteMessageRemove)(nil)

// AIRemoteMessageRemove is a RemoteMessageRemove for redacting AI or user messages.
type AIRemoteMessageRemove struct {
	portal        networkid.PortalKey
	sender        bridgev2.EventSender
	targetMessage networkid.MessageID
}

func (r *AIRemoteMessageRemove) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventMessageRemove
}

func (r *AIRemoteMessageRemove) GetPortalKey() networkid.PortalKey {
	return r.portal
}

func (r *AIRemoteMessageRemove) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("ai_remove_target", string(r.targetMessage))
}

func (r *AIRemoteMessageRemove) GetSender() bridgev2.EventSender {
	return r.sender
}

func (r *AIRemoteMessageRemove) GetTargetMessage() networkid.MessageID {
	return r.targetMessage
}

// -----------------------------------------------------------------------
// Constructor helpers
// -----------------------------------------------------------------------

// NewAITextMessage creates a RemoteMessage for a plain text assistant message.
func NewAITextMessage(
	portal *bridgev2.Portal,
	text string,
	sender bridgev2.EventSender,
) *bridgeadapter.RemoteMessage {
	rendered := msgconv.BuildPlainMessageContent(msgconv.PlainMessageContentParams{
		Text: text,
	})
	return &bridgeadapter.RemoteMessage{
		Portal:    portal.PortalKey,
		ID:        bridgeadapter.NewMessageID("ai"),
		Sender:    sender,
		Timestamp: time.Now(),
		LogKey:    "ai_msg_id",
		PreBuilt: &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{{
				ID:      networkid.PartID("0"),
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgText, Body: text},
				Extra:   rendered.Raw,
			}},
		},
	}
}
