package opencode

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
// OpenCodeRemoteMessage — for sending messages via QueueRemoteEvent
// -----------------------------------------------------------------------

var (
	_ bridgev2.RemoteMessage              = (*OpenCodeRemoteMessage)(nil)
	_ bridgev2.RemoteEventWithTimestamp   = (*OpenCodeRemoteMessage)(nil)
	_ bridgev2.RemoteEventWithStreamOrder = (*OpenCodeRemoteMessage)(nil)
)

// OpenCodeRemoteMessage is a RemoteMessage for OpenCode-generated content routed through bridgev2.
type OpenCodeRemoteMessage struct {
	portal    networkid.PortalKey
	id        networkid.MessageID
	sender    bridgev2.EventSender
	timestamp time.Time

	preBuilt *bridgev2.ConvertedMessage
}

func (m *OpenCodeRemoteMessage) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventMessage
}

func (m *OpenCodeRemoteMessage) GetPortalKey() networkid.PortalKey {
	return m.portal
}

func (m *OpenCodeRemoteMessage) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("opencode_msg_id", string(m.id))
}

func (m *OpenCodeRemoteMessage) GetSender() bridgev2.EventSender {
	return m.sender
}

func (m *OpenCodeRemoteMessage) GetID() networkid.MessageID {
	return m.id
}

func (m *OpenCodeRemoteMessage) GetTimestamp() time.Time {
	if m.timestamp.IsZero() {
		return time.Now()
	}
	return m.timestamp
}

func (m *OpenCodeRemoteMessage) GetStreamOrder() int64 {
	return m.GetTimestamp().UnixMilli()
}

func (m *OpenCodeRemoteMessage) ConvertMessage(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI) (*bridgev2.ConvertedMessage, error) {
	return m.preBuilt, nil
}

// -----------------------------------------------------------------------
// OpenCodeRemoteEdit — for debounced streaming edits
// -----------------------------------------------------------------------

var (
	_ bridgev2.RemoteEdit                 = (*OpenCodeRemoteEdit)(nil)
	_ bridgev2.RemoteEventWithTimestamp   = (*OpenCodeRemoteEdit)(nil)
	_ bridgev2.RemoteEventWithStreamOrder = (*OpenCodeRemoteEdit)(nil)
)

// OpenCodeRemoteEdit is a RemoteEdit for OpenCode streaming response edits.
type OpenCodeRemoteEdit struct {
	portal        networkid.PortalKey
	sender        bridgev2.EventSender
	targetMessage networkid.MessageID
	timestamp     time.Time

	preBuilt *bridgev2.ConvertedEdit
}

func (e *OpenCodeRemoteEdit) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventEdit
}

func (e *OpenCodeRemoteEdit) GetPortalKey() networkid.PortalKey {
	return e.portal
}

func (e *OpenCodeRemoteEdit) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("opencode_edit_target", string(e.targetMessage))
}

func (e *OpenCodeRemoteEdit) GetSender() bridgev2.EventSender {
	return e.sender
}

func (e *OpenCodeRemoteEdit) GetTargetMessage() networkid.MessageID {
	return e.targetMessage
}

func (e *OpenCodeRemoteEdit) GetTimestamp() time.Time {
	if e.timestamp.IsZero() {
		return time.Now()
	}
	return e.timestamp
}

func (e *OpenCodeRemoteEdit) GetStreamOrder() int64 {
	return e.GetTimestamp().UnixMilli()
}

func (e *OpenCodeRemoteEdit) ConvertEdit(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, existing []*database.Message) (*bridgev2.ConvertedEdit, error) {
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

func newOpenCodeMessageID() networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("opencode:%s", uuid.NewString()))
}
