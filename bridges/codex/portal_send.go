package codex

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

// sendViaPortal sends a pre-built message through bridgev2's QueueRemoteEvent pipeline.
// Handles: intent resolution, ghost room join, send, DB persist.
// Returns the Matrix event ID and the network message ID used.
// If msgID is empty, a new one is generated.
func (cc *CodexClient) sendViaPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	converted *bridgev2.ConvertedMessage,
	msgID networkid.MessageID,
) (id.EventID, networkid.MessageID, error) {
	if portal == nil || portal.MXID == "" {
		return "", "", fmt.Errorf("invalid portal")
	}
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return "", msgID, fmt.Errorf("bridge unavailable")
	}
	if msgID == "" {
		msgID = newMessageID()
	}
	sender := cc.senderForPortal()
	evt := &CodexRemoteMessage{
		portal:    portal.PortalKey,
		id:        msgID,
		sender:    sender,
		timestamp: time.Now(),
		preBuilt:  converted,
	}
	result := cc.UserLogin.QueueRemoteEvent(evt)
	if !result.Success {
		if result.Error != nil {
			return "", msgID, fmt.Errorf("send failed: %w", result.Error)
		}
		return "", msgID, fmt.Errorf("send failed")
	}
	return result.EventID, msgID, nil
}

// getCodexIntentForPortal resolves the Matrix intent for the Codex ghost.
// Use this when you need an intent for non-message operations (e.g. UploadMedia, debounced edits).
func (cc *CodexClient) getCodexIntentForPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	evtType bridgev2.RemoteEventType,
) (bridgev2.MatrixAPI, error) {
	sender := cc.senderForPortal()
	intent, ok := portal.GetIntentFor(ctx, sender, cc.UserLogin, evtType)
	if !ok {
		return nil, fmt.Errorf("intent resolution failed")
	}
	return intent, nil
}

// senderForPortal returns the EventSender for the Codex ghost.
func (cc *CodexClient) senderForPortal() bridgev2.EventSender {
	sender := bridgev2.EventSender{Sender: codexGhostID}
	if cc != nil && cc.UserLogin != nil {
		sender.SenderLogin = cc.UserLogin.ID
	}
	return sender
}
