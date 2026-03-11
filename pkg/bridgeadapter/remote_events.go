package bridgeadapter

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

	"github.com/beeper/agentremote/pkg/shared/streamtransport"
)

// -----------------------------------------------------------------------
// RemoteMessage — generic pre-built message for QueueRemoteEvent
// -----------------------------------------------------------------------

var (
	_ bridgev2.RemoteMessage              = (*RemoteMessage)(nil)
	_ bridgev2.RemoteEventWithTimestamp   = (*RemoteMessage)(nil)
	_ bridgev2.RemoteEventWithStreamOrder = (*RemoteMessage)(nil)
)

// RemoteMessage is a bridge-agnostic RemoteMessage implementation backed by pre-built content.
type RemoteMessage struct {
	Portal    networkid.PortalKey
	ID        networkid.MessageID
	Sender    bridgev2.EventSender
	Timestamp time.Time
	PreBuilt  *bridgev2.ConvertedMessage

	// LogKey is the zerolog field name used in AddLogContext (e.g. "ai_msg_id", "codex_msg_id").
	LogKey string
}

func (m *RemoteMessage) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventMessage
}

func (m *RemoteMessage) GetPortalKey() networkid.PortalKey {
	return m.Portal
}

func (m *RemoteMessage) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str(m.LogKey, string(m.ID))
}

func (m *RemoteMessage) GetSender() bridgev2.EventSender {
	return m.Sender
}

func (m *RemoteMessage) GetID() networkid.MessageID {
	return m.ID
}

func (m *RemoteMessage) GetTimestamp() time.Time {
	if m.Timestamp.IsZero() {
		m.Timestamp = time.Now()
	}
	return m.Timestamp
}

func (m *RemoteMessage) GetStreamOrder() int64 {
	return m.GetTimestamp().UnixMilli()
}

func (m *RemoteMessage) ConvertMessage(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI) (*bridgev2.ConvertedMessage, error) {
	return m.PreBuilt, nil
}

// -----------------------------------------------------------------------
// RemoteEdit — generic pre-built edit for QueueRemoteEvent
// -----------------------------------------------------------------------

var (
	_ bridgev2.RemoteEdit                 = (*RemoteEdit)(nil)
	_ bridgev2.RemoteEventWithTimestamp   = (*RemoteEdit)(nil)
	_ bridgev2.RemoteEventWithStreamOrder = (*RemoteEdit)(nil)
)

// RemoteEdit is a bridge-agnostic RemoteEdit implementation backed by pre-built content.
type RemoteEdit struct {
	Portal        networkid.PortalKey
	Sender        bridgev2.EventSender
	TargetMessage networkid.MessageID
	Timestamp     time.Time
	PreBuilt      *bridgev2.ConvertedEdit

	// LogKey is the zerolog field name used in AddLogContext (e.g. "ai_edit_target", "codex_edit_target").
	LogKey string
}

func (e *RemoteEdit) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventEdit
}

func (e *RemoteEdit) GetPortalKey() networkid.PortalKey {
	return e.Portal
}

func (e *RemoteEdit) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str(e.LogKey, string(e.TargetMessage))
}

func (e *RemoteEdit) GetSender() bridgev2.EventSender {
	return e.Sender
}

func (e *RemoteEdit) GetTargetMessage() networkid.MessageID {
	return e.TargetMessage
}

func (e *RemoteEdit) GetTimestamp() time.Time {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	return e.Timestamp
}

func (e *RemoteEdit) GetStreamOrder() int64 {
	return e.GetTimestamp().UnixMilli()
}

func (e *RemoteEdit) ConvertEdit(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, existing []*database.Message) (*bridgev2.ConvertedEdit, error) {
	if e.PreBuilt != nil && len(existing) > 0 {
		for i := range e.PreBuilt.ModifiedParts {
			if e.PreBuilt.ModifiedParts[i].Part == nil && i < len(existing) {
				e.PreBuilt.ModifiedParts[i].Part = existing[i]
			}
		}
	}
	streamtransport.EnsureDontRenderEdited(e.PreBuilt)
	return e.PreBuilt, nil
}

// -----------------------------------------------------------------------
// RemoteReaction — generic reaction for QueueRemoteEvent
// -----------------------------------------------------------------------

var (
	_ bridgev2.RemoteReaction                 = (*RemoteReaction)(nil)
	_ bridgev2.RemoteEventWithTimestamp       = (*RemoteReaction)(nil)
	_ bridgev2.RemoteReactionWithMeta         = (*RemoteReaction)(nil)
	_ bridgev2.RemoteReactionWithExtraContent = (*RemoteReaction)(nil)
)

// RemoteReaction is a bridge-agnostic RemoteReaction implementation.
type RemoteReaction struct {
	Portal        networkid.PortalKey
	Sender        bridgev2.EventSender
	TargetMessage networkid.MessageID
	Emoji         string
	EmojiID       networkid.EmojiID
	Timestamp     time.Time
	DBMeta        *database.Reaction
	ExtraContent  map[string]any

	// LogKey is the zerolog field name used in AddLogContext (e.g. "ai_reaction_target").
	LogKey string
}

func (r *RemoteReaction) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventReaction
}

func (r *RemoteReaction) GetPortalKey() networkid.PortalKey {
	return r.Portal
}

func (r *RemoteReaction) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str(r.LogKey, string(r.TargetMessage)).Str("emoji", r.Emoji)
}

func (r *RemoteReaction) GetSender() bridgev2.EventSender {
	return r.Sender
}

func (r *RemoteReaction) GetTargetMessage() networkid.MessageID {
	return r.TargetMessage
}

func (r *RemoteReaction) GetReactionEmoji() (string, networkid.EmojiID) {
	return variationselector.Add(r.Emoji), r.EmojiID
}

func (r *RemoteReaction) GetTimestamp() time.Time {
	if r.Timestamp.IsZero() {
		return time.Now()
	}
	return r.Timestamp
}

func (r *RemoteReaction) GetReactionDBMetadata() any {
	return r.DBMeta
}

func (r *RemoteReaction) GetReactionExtraContent() map[string]any {
	return r.ExtraContent
}

// -----------------------------------------------------------------------
// RemoteReactionRemove — generic reaction remove for QueueRemoteEvent
// -----------------------------------------------------------------------

var _ bridgev2.RemoteReactionRemove = (*RemoteReactionRemove)(nil)

// RemoteReactionRemove is a bridge-agnostic RemoteReactionRemove implementation.
type RemoteReactionRemove struct {
	Portal        networkid.PortalKey
	Sender        bridgev2.EventSender
	TargetMessage networkid.MessageID
	EmojiID       networkid.EmojiID

	// LogKey is the zerolog field name used in AddLogContext (e.g. "ai_reaction_remove_target").
	LogKey string
}

func (r *RemoteReactionRemove) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventReactionRemove
}

func (r *RemoteReactionRemove) GetPortalKey() networkid.PortalKey {
	return r.Portal
}

func (r *RemoteReactionRemove) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str(r.LogKey, string(r.TargetMessage))
}

func (r *RemoteReactionRemove) GetSender() bridgev2.EventSender {
	return r.Sender
}

func (r *RemoteReactionRemove) GetTargetMessage() networkid.MessageID {
	return r.TargetMessage
}

func (r *RemoteReactionRemove) GetRemovedEmojiID() networkid.EmojiID {
	return r.EmojiID
}

// -----------------------------------------------------------------------
// NewMessageID — generates a unique message ID with the given prefix
// -----------------------------------------------------------------------

// NewMessageID generates a unique message ID in the format "prefix:uuid".
func NewMessageID(prefix string) networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("%s:%s", prefix, uuid.NewString()))
}
