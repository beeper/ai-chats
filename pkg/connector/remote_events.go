package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/variationselector"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/connector/msgconv"
)

// -----------------------------------------------------------------------
// AIRemoteMessage — covers plain text, tool call events, tool result events, media
// -----------------------------------------------------------------------

var (
	_ bridgev2.RemoteMessage                  = (*AIRemoteMessage)(nil)
	_ bridgev2.RemoteEventWithTimestamp       = (*AIRemoteMessage)(nil)
	_ bridgev2.RemoteEventWithStreamOrder     = (*AIRemoteMessage)(nil)
	_ bridgev2.RemoteMessageWithTransactionID = (*AIRemoteMessage)(nil)
)

// AIMessageVariant identifies what kind of Matrix event this message produces.
type AIMessageVariant int

const (
	AIMessageText       AIMessageVariant = iota // Plain assistant text
	AIMessageToolCall                           // Tool call timeline event
	AIMessageToolResult                         // Tool result timeline event
	AIMessageMedia                              // Uploaded media (image, audio, video)
)

// AIRemoteMessage is a RemoteMessage for AI-generated content routed through bridgev2.
type AIRemoteMessage struct {
	portal    networkid.PortalKey
	id        networkid.MessageID
	sender    bridgev2.EventSender
	timestamp time.Time
	txnID     networkid.TransactionID
	variant   AIMessageVariant

	// Pre-built event content — the conversion is done before queuing because
	// AI messages are constructed with full knowledge of what to send (no
	// lazy resolution needed). This is the same pattern as simplevent.PreConvertedMessage.
	preBuilt *bridgev2.ConvertedMessage
}

func (m *AIRemoteMessage) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventMessage
}

func (m *AIRemoteMessage) GetPortalKey() networkid.PortalKey {
	return m.portal
}

func (m *AIRemoteMessage) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("ai_msg_id", string(m.id)).Int("variant", int(m.variant))
}

func (m *AIRemoteMessage) GetSender() bridgev2.EventSender {
	return m.sender
}

func (m *AIRemoteMessage) GetID() networkid.MessageID {
	return m.id
}

func (m *AIRemoteMessage) GetTimestamp() time.Time {
	if m.timestamp.IsZero() {
		return time.Now()
	}
	return m.timestamp
}

func (m *AIRemoteMessage) GetStreamOrder() int64 {
	return m.GetTimestamp().UnixMilli()
}

func (m *AIRemoteMessage) GetTransactionID() networkid.TransactionID {
	return m.txnID
}

func (m *AIRemoteMessage) ConvertMessage(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI) (*bridgev2.ConvertedMessage, error) {
	return m.preBuilt, nil
}

// -----------------------------------------------------------------------
// AIRemoteEdit — for final streaming edits (m.replace)
// -----------------------------------------------------------------------

var (
	_ bridgev2.RemoteEdit                 = (*AIRemoteEdit)(nil)
	_ bridgev2.RemoteEventWithTimestamp   = (*AIRemoteEdit)(nil)
	_ bridgev2.RemoteEventWithStreamOrder = (*AIRemoteEdit)(nil)
)

// AIRemoteEdit is a RemoteEdit for the final streaming response edit.
type AIRemoteEdit struct {
	portal        networkid.PortalKey
	sender        bridgev2.EventSender
	targetMessage networkid.MessageID
	timestamp     time.Time

	// Pre-built edit content.
	preBuilt *bridgev2.ConvertedEdit
}

func (e *AIRemoteEdit) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventEdit
}

func (e *AIRemoteEdit) GetPortalKey() networkid.PortalKey {
	return e.portal
}

func (e *AIRemoteEdit) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("ai_edit_target", string(e.targetMessage))
}

func (e *AIRemoteEdit) GetSender() bridgev2.EventSender {
	return e.sender
}

func (e *AIRemoteEdit) GetTargetMessage() networkid.MessageID {
	return e.targetMessage
}

func (e *AIRemoteEdit) GetTimestamp() time.Time {
	if e.timestamp.IsZero() {
		return time.Now()
	}
	return e.timestamp
}

func (e *AIRemoteEdit) GetStreamOrder() int64 {
	return e.GetTimestamp().UnixMilli()
}

func (e *AIRemoteEdit) ConvertEdit(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, _ []*database.Message) (*bridgev2.ConvertedEdit, error) {
	return e.preBuilt, nil
}

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
// AIRemoteTyping — for typing indicators
// -----------------------------------------------------------------------

var _ bridgev2.RemoteTyping = (*AIRemoteTyping)(nil)

// AIRemoteTyping is a RemoteTyping for AI typing indicators.
type AIRemoteTyping struct {
	portal  networkid.PortalKey
	sender  bridgev2.EventSender
	timeout time.Duration
}

func (t *AIRemoteTyping) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventTyping
}

func (t *AIRemoteTyping) GetPortalKey() networkid.PortalKey {
	return t.portal
}

func (t *AIRemoteTyping) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Dur("typing_timeout", t.timeout)
}

func (t *AIRemoteTyping) GetSender() bridgev2.EventSender {
	return t.sender
}

func (t *AIRemoteTyping) GetTimeout() time.Duration {
	return t.timeout
}

// -----------------------------------------------------------------------
// Constructor helpers — build pre-converted messages for common patterns
// -----------------------------------------------------------------------

// NewAITextMessage creates an AIRemoteMessage for a plain text assistant message.
func NewAITextMessage(
	portal *bridgev2.Portal,
	login *bridgev2.UserLogin,
	text string,
	meta *PortalMetadata,
	agentID string,
	modelID string,
) *AIRemoteMessage {
	rendered := msgconv.BuildPlainMessageContent(msgconv.PlainMessageContentParams{
		Text: text,
	})
	senderID := modelUserID(modelID)
	if agentID != "" {
		senderID = agentUserID(agentID)
	}
	return &AIRemoteMessage{
		portal:    portal.PortalKey,
		id:        newMessageID(),
		sender:    bridgev2.EventSender{Sender: senderID, SenderLogin: login.ID},
		timestamp: time.Now(),
		variant:   AIMessageText,
		preBuilt: &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{{
				ID:      networkid.PartID("0"),
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgText, Body: text},
				Extra:   rendered.Raw,
			}},
		},
	}
}

// NewAIToolCallMessage creates an AIRemoteMessage for a tool call timeline event.
func NewAIToolCallMessage(
	portal *bridgev2.Portal,
	login *bridgev2.UserLogin,
	params msgconv.ToolCallEventParams,
	modelID string,
) *AIRemoteMessage {
	content := msgconv.BuildToolCallEventContent(params)
	senderID := modelUserID(modelID)
	return &AIRemoteMessage{
		portal:    portal.PortalKey,
		id:        newMessageID(),
		sender:    bridgev2.EventSender{Sender: senderID, SenderLogin: login.ID},
		timestamp: time.Now(),
		variant:   AIMessageToolCall,
		preBuilt: &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{{
				ID:    networkid.PartID("0"),
				Type:  ToolCallEventType,
				Extra: content.Raw,
			}},
		},
	}
}

// NewAIToolResultMessage creates an AIRemoteMessage for a tool result timeline event.
func NewAIToolResultMessage(
	portal *bridgev2.Portal,
	login *bridgev2.UserLogin,
	params msgconv.ToolResultEventParams,
	modelID string,
) *AIRemoteMessage {
	content := msgconv.BuildToolResultEventContent(params)
	senderID := modelUserID(modelID)
	return &AIRemoteMessage{
		portal:    portal.PortalKey,
		id:        newMessageID(),
		sender:    bridgev2.EventSender{Sender: senderID, SenderLogin: login.ID},
		timestamp: time.Now(),
		variant:   AIMessageToolResult,
		preBuilt: &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{{
				ID:    networkid.PartID("0"),
				Type:  ToolResultEventType,
				Extra: content.Raw,
			}},
		},
	}
}

// newMessageID generates a unique message ID for AI remote events.
func newMessageID() networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("ai:%s", uuid.NewString()))
}
