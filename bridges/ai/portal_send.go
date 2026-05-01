package ai

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) senderForPortal(ctx context.Context, portal *bridgev2.Portal) bridgev2.EventSender {
	if portal != nil && portal.OtherUserID != "" {
		return bridgev2.EventSender{Sender: portal.OtherUserID, SenderLogin: oc.UserLogin.ID}
	}
	meta := portalMeta(portal)
	if override, ok := modelOverrideFromContext(ctx); ok {
		if meta == nil {
			meta = &PortalMetadata{RuntimeModelOverride: override}
		} else {
			cloned := *meta
			cloned.RuntimeModelOverride = override
			meta = &cloned
		}
	}
	responder := oc.responderForMeta(ctx, meta)
	senderID := networkid.UserID("")
	if responder != nil {
		senderID = responder.GhostID
	} else if meta != nil {
		if agentID := resolveAgentID(meta); agentID != "" {
			senderID = oc.agentUserID(agentID)
		} else if modelID := oc.effectiveModel(meta); modelID != "" {
			senderID = modelUserID(modelID)
		}
	}
	return bridgev2.EventSender{Sender: senderID, SenderLogin: oc.UserLogin.ID}
}

func (oc *AIClient) redactEventViaPortal(ctx context.Context, portal *bridgev2.Portal, eventID id.EventID) error {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return fmt.Errorf("bridge unavailable")
	}
	if portal == nil || portal.MXID == "" || eventID == "" {
		return fmt.Errorf("invalid portal or event ID")
	}
	part, err := oc.loadPortalMessagePartByMXID(ctx, portal, eventID)
	if err != nil {
		return fmt.Errorf("message lookup failed: %w", err)
	}
	if part == nil {
		return fmt.Errorf("message not found for event %s", eventID)
	}
	sender := oc.senderForPortal(ctx, portal)
	result := oc.UserLogin.QueueRemoteEvent(&simplevent.MessageRemove{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessageRemove,
			PortalKey: portal.PortalKey,
			Sender:    sender,
		},
		TargetMessage: part.ID,
	})
	if !result.Success {
		if result.Error != nil {
			return fmt.Errorf("redact failed: %w", result.Error)
		}
		return fmt.Errorf("redact failed")
	}
	return nil
}

func (oc *AIClient) redactNetworkMessageViaPortal(ctx context.Context, portal *bridgev2.Portal, targetMessageID networkid.MessageID) error {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return fmt.Errorf("bridge unavailable")
	}
	if portal == nil || portal.MXID == "" || targetMessageID == "" {
		return fmt.Errorf("invalid portal or message ID")
	}
	sender := oc.senderForPortal(ctx, portal)
	result := oc.UserLogin.QueueRemoteEvent(&simplevent.MessageRemove{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessageRemove,
			PortalKey: portal.PortalKey,
			Sender:    sender,
		},
		TargetMessage: targetMessageID,
	})
	if !result.Success {
		if result.Error != nil {
			return fmt.Errorf("redact failed: %w", result.Error)
		}
		return fmt.Errorf("redact failed")
	}
	return nil
}
