package sdk

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/ai-chats/turns"
)

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
	// StreamOrder overrides timestamp-based ordering when the caller has a stable upstream order.
	StreamOrder int64
	PreBuilt    *bridgev2.ConvertedEdit
	DBMetadata  any

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
	if e.StreamOrder != 0 {
		return e.StreamOrder
	}
	return ResolveEventTiming(e.GetTimestamp(), 0).StreamOrder
}

func (e *RemoteEdit) ConvertEdit(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, existing []*database.Message) (*bridgev2.ConvertedEdit, error) {
	if e.PreBuilt != nil && len(existing) > 0 {
		for i := range e.PreBuilt.ModifiedParts {
			if e.PreBuilt.ModifiedParts[i].Part == nil && i < len(existing) {
				e.PreBuilt.ModifiedParts[i].Part = existing[i]
			}
			if e.DBMetadata != nil && e.PreBuilt.ModifiedParts[i].Part != nil {
				e.PreBuilt.ModifiedParts[i].Part.Metadata = e.DBMetadata
			}
		}
	}
	turns.EnsureDontRenderEdited(e.PreBuilt)
	return e.PreBuilt, nil
}

// NewMessageID generates a unique message ID in the format "prefix:uuid".
func NewMessageID(prefix string) networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("%s:%s", prefix, uuid.NewString()))
}
