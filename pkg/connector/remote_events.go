package connector

import (
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/variationselector"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"github.com/beeper/ai-bridge/pkg/connector/msgconv"
)

// -----------------------------------------------------------------------
// AIRemoteReaction — for AI-sent reactions
// -----------------------------------------------------------------------

var (
	_ bridgev2.RemoteReaction                 = (*AIRemoteReaction)(nil)
	_ bridgev2.RemoteEventWithTimestamp       = (*AIRemoteReaction)(nil)
	_ bridgev2.RemoteReactionWithMeta         = (*AIRemoteReaction)(nil)
	_ bridgev2.RemoteReactionWithExtraContent = (*AIRemoteReaction)(nil)
)

// AIRemoteReaction is a RemoteReaction for AI-generated reactions.
type AIRemoteReaction struct {
	portal        networkid.PortalKey
	sender        bridgev2.EventSender
	targetMessage networkid.MessageID
	emoji         string
	emojiID       networkid.EmojiID
	timestamp     time.Time
	dbMeta        *database.Reaction
}

func (r *AIRemoteReaction) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventReaction
}

func (r *AIRemoteReaction) GetPortalKey() networkid.PortalKey {
	return r.portal
}

func (r *AIRemoteReaction) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("ai_reaction_target", string(r.targetMessage)).Str("emoji", r.emoji)
}

func (r *AIRemoteReaction) GetSender() bridgev2.EventSender {
	return r.sender
}

func (r *AIRemoteReaction) GetTargetMessage() networkid.MessageID {
	return r.targetMessage
}

func (r *AIRemoteReaction) GetReactionEmoji() (string, networkid.EmojiID) {
	return variationselector.Add(r.emoji), r.emojiID
}

func (r *AIRemoteReaction) GetTimestamp() time.Time {
	if r.timestamp.IsZero() {
		return time.Now()
	}
	return r.timestamp
}

func (r *AIRemoteReaction) GetReactionDBMetadata() any {
	return r.dbMeta
}

func (r *AIRemoteReaction) GetReactionExtraContent() map[string]any {
	return nil
}

// -----------------------------------------------------------------------
// AIRemoteReactionRemove — for removing AI reactions
// -----------------------------------------------------------------------

var _ bridgev2.RemoteReactionRemove = (*AIRemoteReactionRemove)(nil)

// AIRemoteReactionRemove is a RemoteReactionRemove for removing AI reactions.
type AIRemoteReactionRemove struct {
	portal        networkid.PortalKey
	sender        bridgev2.EventSender
	targetMessage networkid.MessageID
	emojiID       networkid.EmojiID
}

func (r *AIRemoteReactionRemove) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventReactionRemove
}

func (r *AIRemoteReactionRemove) GetPortalKey() networkid.PortalKey {
	return r.portal
}

func (r *AIRemoteReactionRemove) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("ai_reaction_remove_target", string(r.targetMessage))
}

func (r *AIRemoteReactionRemove) GetSender() bridgev2.EventSender {
	return r.sender
}

func (r *AIRemoteReactionRemove) GetTargetMessage() networkid.MessageID {
	return r.targetMessage
}

func (r *AIRemoteReactionRemove) GetRemovedEmojiID() networkid.EmojiID {
	return r.emojiID
}

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
	login *bridgev2.UserLogin,
	text string,
	meta *PortalMetadata,
	agentID string,
	modelID string,
) *bridgeadapter.RemoteMessage {
	rendered := msgconv.BuildPlainMessageContent(msgconv.PlainMessageContentParams{
		Text: text,
	})
	senderID := modelUserID(modelID)
	if agentID != "" {
		senderID = agentUserID(agentID)
	}
	return &bridgeadapter.RemoteMessage{
		Portal:    portal.PortalKey,
		ID:        bridgeadapter.NewMessageID("ai"),
		Sender:    bridgev2.EventSender{Sender: senderID, SenderLogin: login.ID},
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

// newMessageID generates a unique message ID for AI remote events.
func newMessageID() networkid.MessageID {
	return bridgeadapter.NewMessageID("ai")
}
