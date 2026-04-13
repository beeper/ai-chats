package sdk

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

type PreConvertedRemoteMessageParams struct {
	PortalKey   networkid.PortalKey
	Sender      bridgev2.EventSender
	MsgID       networkid.MessageID
	IDPrefix    string
	LogKey      string
	Timestamp   time.Time
	StreamOrder int64
	Converted   *bridgev2.ConvertedMessage
}

func BuildPreConvertedRemoteMessage(p PreConvertedRemoteMessageParams) *simplevent.PreConvertedMessage {
	if p.MsgID == "" {
		p.MsgID = NewMessageID(p.IDPrefix)
	}
	timing := ResolveEventTiming(p.Timestamp, p.StreamOrder)
	return &simplevent.PreConvertedMessage{
		EventMeta: simplevent.EventMeta{
			Type:        bridgev2.RemoteEventMessage,
			PortalKey:   p.PortalKey,
			Sender:      p.Sender,
			Timestamp:   timing.Timestamp,
			StreamOrder: timing.StreamOrder,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str(p.LogKey, string(p.MsgID))
			},
		},
		ID:   p.MsgID,
		Data: p.Converted,
	}
}

// SendViaPortalParams holds the parameters for SendViaPortal.
type SendViaPortalParams struct {
	Login       *bridgev2.UserLogin
	Portal      *bridgev2.Portal
	Sender      bridgev2.EventSender
	IDPrefix    string
	LogKey      string
	MsgID       networkid.MessageID
	Timestamp   time.Time
	StreamOrder int64
	Converted   *bridgev2.ConvertedMessage
}

// SendViaPortal sends a pre-built message through bridgev2's QueueRemoteEvent pipeline.
// If MsgID is empty, a new one is generated using IDPrefix.
func SendViaPortal(p SendViaPortalParams) (id.EventID, networkid.MessageID, error) {
	if p.Portal == nil || p.Portal.MXID == "" {
		return "", "", fmt.Errorf("invalid portal")
	}
	if p.Login == nil || p.Login.Bridge == nil {
		return "", p.MsgID, fmt.Errorf("bridge unavailable")
	}
	evt := BuildPreConvertedRemoteMessage(PreConvertedRemoteMessageParams{
		PortalKey:   p.Portal.PortalKey,
		Sender:      p.Sender,
		MsgID:       p.MsgID,
		IDPrefix:    p.IDPrefix,
		LogKey:      p.LogKey,
		Timestamp:   p.Timestamp,
		StreamOrder: p.StreamOrder,
		Converted:   p.Converted,
	})
	result := p.Login.QueueRemoteEvent(evt)
	if !result.Success {
		if result.Error != nil {
			return "", evt.ID, fmt.Errorf("send failed: %w", result.Error)
		}
		return "", evt.ID, fmt.Errorf("send failed")
	}
	return result.EventID, evt.ID, nil
}

// SendEditViaPortal queues a pre-built edit through bridgev2's remote event pipeline.
func SendEditViaPortal(
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	targetMessage networkid.MessageID,
	timestamp time.Time,
	streamOrder int64,
	logKey string,
	converted *bridgev2.ConvertedEdit,
) error {
	if portal == nil || portal.MXID == "" {
		return fmt.Errorf("invalid portal")
	}
	if login == nil || login.Bridge == nil {
		return fmt.Errorf("bridge unavailable")
	}
	if targetMessage == "" {
		return fmt.Errorf("invalid target message")
	}
	timing := ResolveEventTiming(timestamp, streamOrder)
	result := login.QueueRemoteEvent(&RemoteEdit{
		Portal:        portal.PortalKey,
		Sender:        sender,
		TargetMessage: targetMessage,
		Timestamp:     timing.Timestamp,
		StreamOrder:   timing.StreamOrder,
		LogKey:        logKey,
		PreBuilt:      converted,
	})
	if !result.Success {
		if result.Error != nil {
			return fmt.Errorf("edit failed: %w", result.Error)
		}
		return fmt.Errorf("edit failed")
	}
	return nil
}

// RedactEventAsSender redacts an event ID in a room using the intent resolved for sender.
func RedactEventAsSender(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	targetEventID id.EventID,
) error {
	if login == nil || portal == nil || portal.MXID == "" || targetEventID == "" {
		return fmt.Errorf("invalid redaction target")
	}
	intent, ok := portal.GetIntentFor(ctx, sender, login, bridgev2.RemoteEventMessageRemove)
	if !ok || intent == nil {
		return fmt.Errorf("intent resolution failed")
	}
	_, err := intent.SendMessage(ctx, portal.MXID, event.EventRedaction, &event.Content{
		Parsed: &event.RedactionEventContent{Redacts: targetEventID},
	}, nil)
	return err
}

func SendSystemMessage(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	body string,
) error {
	body = strings.TrimSpace(body)
	if login == nil || login.Bridge == nil {
		return fmt.Errorf("bridge unavailable")
	}
	if portal == nil || portal.MXID == "" {
		return fmt.Errorf("invalid portal")
	}
	if body == "" {
		return nil
	}
	content := &event.Content{
		Parsed: &event.MessageEventContent{
			MsgType:  event.MsgNotice,
			Body:     body,
			Mentions: &event.Mentions{},
		},
	}
	if login.Bridge.Bot != nil {
		_, err := login.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, content, nil)
		return err
	}
	intent, ok := portal.GetIntentFor(ctx, sender, login, bridgev2.RemoteEventMessage)
	if !ok || intent == nil {
		return fmt.Errorf("intent resolution failed")
	}
	_, err := intent.SendMessage(ctx, portal.MXID, event.EventMessage, content, nil)
	return err
}

// BuildContinuationMessage constructs a ConvertedMessage for overflow
// continuation text, flagged with "com.beeper.continuation".
func BuildContinuationMessage(
	portal networkid.PortalKey,
	body string,
	sender bridgev2.EventSender,
	idPrefix,
	logKey string,
	timestamp time.Time,
	streamOrder int64,
) *simplevent.PreConvertedMessage {
	rendered := format.RenderMarkdown(body, true, true)
	content := &event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          rendered.Body,
		Format:        rendered.Format,
		FormattedBody: rendered.FormattedBody,
		Mentions:      &event.Mentions{},
	}
	return BuildPreConvertedRemoteMessage(PreConvertedRemoteMessageParams{
		PortalKey:   portal,
		Sender:      sender,
		IDPrefix:    idPrefix,
		LogKey:      logKey,
		Timestamp:   timestamp,
		StreamOrder: streamOrder,
		Converted: &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{{
				ID:      networkid.PartID("0"),
				Type:    event.EventMessage,
				Content: content,
				Extra:   map[string]any{"com.beeper.continuation": true},
			}},
		},
	})
}
