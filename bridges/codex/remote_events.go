package codex

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// -----------------------------------------------------------------------
// CodexRemoteMessage — covers plain text, tool call events
// -----------------------------------------------------------------------

var (
	_ bridgev2.RemoteMessage              = (*CodexRemoteMessage)(nil)
	_ bridgev2.RemoteEventWithTimestamp   = (*CodexRemoteMessage)(nil)
	_ bridgev2.RemoteEventWithStreamOrder = (*CodexRemoteMessage)(nil)
)

// CodexRemoteMessage is a RemoteMessage for Codex-generated content routed through bridgev2.
type CodexRemoteMessage struct {
	portal    networkid.PortalKey
	id        networkid.MessageID
	sender    bridgev2.EventSender
	timestamp time.Time

	// Pre-built event content.
	preBuilt *bridgev2.ConvertedMessage
}

func (m *CodexRemoteMessage) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventMessage
}

func (m *CodexRemoteMessage) GetPortalKey() networkid.PortalKey {
	return m.portal
}

func (m *CodexRemoteMessage) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("codex_msg_id", string(m.id))
}

func (m *CodexRemoteMessage) GetSender() bridgev2.EventSender {
	return m.sender
}

func (m *CodexRemoteMessage) GetID() networkid.MessageID {
	return m.id
}

func (m *CodexRemoteMessage) GetTimestamp() time.Time {
	if m.timestamp.IsZero() {
		return time.Now()
	}
	return m.timestamp
}

func (m *CodexRemoteMessage) GetStreamOrder() int64 {
	return m.GetTimestamp().UnixMilli()
}

func (m *CodexRemoteMessage) ConvertMessage(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI) (*bridgev2.ConvertedMessage, error) {
	return m.preBuilt, nil
}

// -----------------------------------------------------------------------
// CodexRemoteEdit — for final streaming edits (m.replace)
// -----------------------------------------------------------------------

var (
	_ bridgev2.RemoteEdit                 = (*CodexRemoteEdit)(nil)
	_ bridgev2.RemoteEventWithTimestamp   = (*CodexRemoteEdit)(nil)
	_ bridgev2.RemoteEventWithStreamOrder = (*CodexRemoteEdit)(nil)
)

// CodexRemoteEdit is a RemoteEdit for the final streaming response edit.
type CodexRemoteEdit struct {
	portal        networkid.PortalKey
	sender        bridgev2.EventSender
	targetMessage networkid.MessageID
	timestamp     time.Time

	// Pre-built edit content.
	preBuilt *bridgev2.ConvertedEdit
}

func (e *CodexRemoteEdit) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventEdit
}

func (e *CodexRemoteEdit) GetPortalKey() networkid.PortalKey {
	return e.portal
}

func (e *CodexRemoteEdit) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("codex_edit_target", string(e.targetMessage))
}

func (e *CodexRemoteEdit) GetSender() bridgev2.EventSender {
	return e.sender
}

func (e *CodexRemoteEdit) GetTargetMessage() networkid.MessageID {
	return e.targetMessage
}

func (e *CodexRemoteEdit) GetTimestamp() time.Time {
	if e.timestamp.IsZero() {
		return time.Now()
	}
	return e.timestamp
}

func (e *CodexRemoteEdit) GetStreamOrder() int64 {
	return e.GetTimestamp().UnixMilli()
}

func (e *CodexRemoteEdit) ConvertEdit(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, existing []*database.Message) (*bridgev2.ConvertedEdit, error) {
	// Bind existing DB parts to modified parts when Part was left nil at build time.
	if e.preBuilt != nil && len(existing) > 0 {
		for i, part := range e.preBuilt.ModifiedParts {
			if part.Part == nil && i < len(existing) {
				part.Part = existing[i]
			}
		}
	}
	return e.preBuilt, nil
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

// newMessageID generates a unique message ID for Codex remote events.
func newMessageID() networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("codex:%s", uuid.NewString()))
}
