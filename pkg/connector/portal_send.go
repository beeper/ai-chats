package connector

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

// Handles: intent resolution, ghost room join, send, DB persist.
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
	sender := oc.senderForPortal(ctx, portal)
	pi := portal.Internal()
	intent, _, err := pi.GetIntentAndUserMXIDFor(
		ctx, sender, oc.UserLogin, nil, bridgev2.RemoteEventMessage,
	)
	if err != nil {
		return "", "", fmt.Errorf("intent resolution failed: %w", err)
	}
	if msgID == "" {
		msgID = newMessageID()
	}
	now := time.Now()
	dbMsgs, result := pi.SendConvertedMessage(
		ctx, msgID, intent, sender.Sender, converted,
		now, now.UnixMilli(), nil,
	)
	if !result.Success {
		if result.Error != nil {
			return "", msgID, fmt.Errorf("send failed: %w", result.Error)
		}
		return "", msgID, fmt.Errorf("send failed")
	}
	if len(dbMsgs) == 0 {
		return "", msgID, fmt.Errorf("send returned no messages")
	}
	return dbMsgs[0].MXID, msgID, nil
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
	pi := portal.Internal()
	intent, _, err := pi.GetIntentAndUserMXIDFor(
		ctx, sender, oc.UserLogin, nil, bridgev2.RemoteEventEdit,
	)
	if err != nil {
		return fmt.Errorf("intent resolution failed: %w", err)
	}
	now := time.Now()
	result := pi.SendConvertedEdit(
		ctx, targetMsgID, sender.Sender, converted, intent,
		now, now.UnixMilli(),
	)
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
	receiver := portal.Receiver
	if receiver == "" {
		receiver = oc.UserLogin.ID
	}
	parts, err := oc.UserLogin.Bridge.DB.Message.GetAllPartsByID(ctx, receiver, targetMsgID)
	if err != nil {
		return fmt.Errorf("target not found: %w", err)
	}
	if len(parts) == 0 {
		return fmt.Errorf("no message parts found for %s", targetMsgID)
	}
	sender := oc.senderForPortal(ctx, portal)
	pi := portal.Internal()
	intent, _, err := pi.GetIntentAndUserMXIDFor(
		ctx, sender, oc.UserLogin, nil, bridgev2.RemoteEventMessageRemove,
	)
	if err != nil {
		return fmt.Errorf("intent resolution failed: %w", err)
	}
	result := pi.RedactMessageParts(ctx, parts, intent, time.Now())
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
	pi := portal.Internal()
	intent, _, err := pi.GetIntentAndUserMXIDFor(
		ctx, sender, oc.UserLogin, nil, evtType,
	)
	if err != nil {
		return nil, fmt.Errorf("intent resolution failed: %w", err)
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
