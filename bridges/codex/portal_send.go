package codex

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

// sendViaPortal sends a pre-built message through bridgev2's full pipeline.
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
	sender := cc.senderForPortal()
	pi := portal.Internal()
	intent, _, err := pi.GetIntentAndUserMXIDFor(
		ctx, sender, cc.UserLogin, nil, bridgev2.RemoteEventMessage,
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

// getCodexIntentForPortal resolves the Matrix intent for the Codex ghost.
// Use this when you need an intent for non-message operations (e.g. UploadMedia, debounced edits).
func (cc *CodexClient) getCodexIntentForPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	evtType bridgev2.RemoteEventType,
) (bridgev2.MatrixAPI, error) {
	sender := cc.senderForPortal()
	pi := portal.Internal()
	intent, _, err := pi.GetIntentAndUserMXIDFor(
		ctx, sender, cc.UserLogin, nil, evtType,
	)
	if err != nil {
		return nil, fmt.Errorf("intent resolution failed: %w", err)
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
