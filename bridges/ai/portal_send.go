package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
)

type portalIntentGetter func(context.Context, *bridgev2.Portal, bridgev2.EventSender, bridgev2.RemoteEventType) (bridgev2.MatrixAPI, error)

func ensureConvertedMessageParts(converted *bridgev2.ConvertedMessage) {
	if converted == nil || len(converted.Parts) == 0 {
		return
	}
	parts := converted.Parts[:0]
	for _, part := range converted.Parts {
		if part == nil {
			continue
		}
		if part.Content == nil {
			part.Content = &event.MessageEventContent{}
		}
		parts = append(parts, part)
	}
	converted.Parts = parts
}

func resolvePortalSenderAndIntent(
	ctx context.Context,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	evtType bridgev2.RemoteEventType,
	ensureJoined bool,
	getIntent portalIntentGetter,
) (bridgev2.EventSender, bridgev2.MatrixAPI, error) {
	if portal == nil || portal.MXID == "" {
		return sender, nil, fmt.Errorf("invalid portal")
	}
	if getIntent == nil {
		return sender, nil, fmt.Errorf("intent resolution unavailable")
	}
	intent, err := getIntent(ctx, portal, sender, evtType)
	if err != nil {
		return sender, nil, err
	}
	if intent == nil {
		return sender, nil, fmt.Errorf("intent resolution failed")
	}
	if ensureJoined {
		if err = intent.EnsureJoined(ctx, portal.MXID); err != nil {
			return sender, nil, fmt.Errorf("ensure joined failed: %w", err)
		}
	}
	return sender, intent, nil
}

func (oc *AIClient) resolvePortalSenderAndIntent(
	ctx context.Context,
	portal *bridgev2.Portal,
	evtType bridgev2.RemoteEventType,
	ensureJoined bool,
) (bridgev2.EventSender, bridgev2.MatrixAPI, error) {
	sender := oc.senderForPortal(ctx, portal)
	return resolvePortalSenderAndIntent(ctx, portal, sender, evtType, ensureJoined, oc.getIntentForSender)
}

// sendViaPortal sends a pre-built message through bridgev2's QueueRemoteEvent pipeline.
func (oc *AIClient) sendViaPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	converted *bridgev2.ConvertedMessage,
	msgID networkid.MessageID,
) (id.EventID, networkid.MessageID, error) {
	return oc.sendViaPortalWithTiming(ctx, portal, converted, msgID, time.Now(), 0)
}

func (oc *AIClient) sendViaPortalWithTiming(
	ctx context.Context,
	portal *bridgev2.Portal,
	converted *bridgev2.ConvertedMessage,
	msgID networkid.MessageID,
	timestamp time.Time,
	streamOrder int64,
) (id.EventID, networkid.MessageID, error) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return "", "", fmt.Errorf("bridge unavailable")
	}
	if portal == nil || portal.MXID == "" {
		return "", "", fmt.Errorf("invalid portal")
	}
	ensureConvertedMessageParts(converted)
	sender, _, err := oc.resolvePortalSenderAndIntent(ctx, portal, bridgev2.RemoteEventMessage, true)
	if err != nil {
		return "", "", err
	}
	return oc.ClientBase.SendViaPortalWithOptions(portal, sender, msgID, timestamp, streamOrder, converted)
}

// The targetMsgID is the network message ID of the message to edit.
func (oc *AIClient) sendEditViaPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	targetMsgID networkid.MessageID,
	converted *bridgev2.ConvertedEdit,
) error {
	return oc.sendEditViaPortalWithTiming(ctx, portal, targetMsgID, converted, time.Now(), 0)
}

func (oc *AIClient) sendEditViaPortalWithTiming(
	ctx context.Context,
	portal *bridgev2.Portal,
	targetMsgID networkid.MessageID,
	converted *bridgev2.ConvertedEdit,
	timestamp time.Time,
	streamOrder int64,
) error {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return fmt.Errorf("bridge unavailable")
	}
	if portal == nil || portal.MXID == "" {
		return fmt.Errorf("invalid portal")
	}
	if targetMsgID == "" {
		return fmt.Errorf("invalid target message")
	}
	sender, _, err := oc.resolvePortalSenderAndIntent(ctx, portal, bridgev2.RemoteEventMessage, true)
	if err != nil {
		return err
	}
	return agentremote.SendEditViaPortal(oc.UserLogin, portal, sender, targetMsgID, timestamp, streamOrder, "ai_edit_target", converted)
}

func (oc *AIClient) redactViaPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	targetMsgID networkid.MessageID,
) error {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return fmt.Errorf("bridge unavailable")
	}
	if portal == nil || portal.MXID == "" {
		return fmt.Errorf("invalid portal")
	}
	sender, _, err := oc.resolvePortalSenderAndIntent(ctx, portal, bridgev2.RemoteEventMessage, true)
	if err != nil {
		return err
	}
	evt := &simplevent.MessageRemove{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessageRemove,
			PortalKey: portal.PortalKey,
			Sender:    sender,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str("ai_remove_target", string(targetMsgID))
			},
		},
		TargetMessage: targetMsgID,
	}
	result := oc.UserLogin.QueueRemoteEvent(evt)
	if !result.Success {
		if result.Error != nil {
			return fmt.Errorf("redact failed: %w", result.Error)
		}
		return fmt.Errorf("redact failed")
	}
	return nil
}

// redactEventViaPortal redacts a single Matrix event by its event ID through bridgev2's pipeline.
// Unlike redactViaPortal, this looks up the message by Matrix event ID rather than network message ID.
func (oc *AIClient) redactEventViaPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	eventID id.EventID,
) error {
	if portal == nil || portal.MXID == "" || eventID == "" {
		return fmt.Errorf("invalid portal or event ID")
	}
	part, err := oc.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, eventID)
	if err != nil {
		return fmt.Errorf("message lookup failed: %w", err)
	}
	if part == nil {
		return fmt.Errorf("message not found for event %s", eventID)
	}
	return oc.redactViaPortal(ctx, portal, part.ID)
}

// Use this when you need an intent for non-message operations (e.g. UploadMedia).
func (oc *AIClient) getIntentForPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	evtType bridgev2.RemoteEventType,
) (bridgev2.MatrixAPI, error) {
	sender := oc.senderForPortal(ctx, portal)
	return oc.getIntentForSender(ctx, portal, sender, evtType)
}

func (oc *AIClient) getIntentForSender(
	ctx context.Context,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	evtType bridgev2.RemoteEventType,
) (bridgev2.MatrixAPI, error) {
	intent, ok := portal.GetIntentFor(ctx, sender, oc.UserLogin, evtType)
	if !ok {
		return nil, fmt.Errorf("intent resolution failed")
	}
	return intent, nil
}

func (oc *AIClient) senderForPortal(ctx context.Context, portal *bridgev2.Portal) bridgev2.EventSender {
	meta := portalMeta(portal)
	agentID := resolveAgentID(meta)
	modelID := oc.effectiveModel(meta)
	if agentID == "" {
		if override, ok := modelOverrideFromContext(ctx); ok {
			modelID = override
		}
	}
	senderID := modelUserID(modelID)
	if agentID != "" {
		senderID = oc.agentUserID(agentID)
	}
	return bridgev2.EventSender{Sender: senderID, SenderLogin: oc.UserLogin.ID}
}
