package connector

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

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

// Handles: intent resolution, ghost room join, send, DB persist via QueueRemoteEvent.
// Returns the Matrix event ID and the network message ID used.
// If msgID is empty, a new one is generated.
func (oc *AIClient) sendViaPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	converted *bridgev2.ConvertedMessage,
	msgID networkid.MessageID,
) (id.EventID, networkid.MessageID, error) {
	if portal == nil || portal.MXID == "" {
		return "", "", fmt.Errorf("invalid portal")
	}
	if msgID == "" {
		msgID = newMessageID()
	}
	ensureConvertedMessageParts(converted)
	sender := oc.senderForPortal(ctx, portal)
	evt := &bridgeadapter.RemoteMessage{
		Portal:    portal.PortalKey,
		ID:        msgID,
		Sender:    sender,
		Timestamp: time.Now(),
		LogKey:    "ai_msg_id",
		PreBuilt:  converted,
	}
	result := oc.UserLogin.QueueRemoteEvent(evt)
	if !result.Success {
		if result.Error != nil {
			return "", msgID, fmt.Errorf("send failed: %w", result.Error)
		}
		return "", msgID, fmt.Errorf("send failed")
	}
	return result.EventID, msgID, nil
}

// The targetMsgID is the network message ID of the message to edit.
func (oc *AIClient) sendEditViaPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	targetMsgID networkid.MessageID,
	converted *bridgev2.ConvertedEdit,
) error {
	if portal == nil || portal.MXID == "" {
		return fmt.Errorf("invalid portal")
	}
	sender := oc.senderForPortal(ctx, portal)
	evt := &bridgeadapter.RemoteEdit{
		Portal:        portal.PortalKey,
		Sender:        sender,
		TargetMessage: targetMsgID,
		Timestamp:     time.Now(),
		LogKey:        "ai_edit_target",
		PreBuilt:      converted,
	}
	result := oc.UserLogin.QueueRemoteEvent(evt)
	if !result.Success {
		if result.Error != nil {
			return fmt.Errorf("edit failed: %w", result.Error)
		}
		return fmt.Errorf("edit failed")
	}
	return nil
}

func (oc *AIClient) redactViaPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	targetMsgID networkid.MessageID,
) error {
	if portal == nil || portal.MXID == "" {
		return fmt.Errorf("invalid portal")
	}
	sender := oc.senderForPortal(ctx, portal)
	evt := &AIRemoteMessageRemove{
		portal:        portal.PortalKey,
		sender:        sender,
		targetMessage: targetMsgID,
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
		senderID = agentUserID(agentID)
	}
	return bridgev2.EventSender{Sender: senderID, SenderLogin: oc.UserLogin.ID}
}
