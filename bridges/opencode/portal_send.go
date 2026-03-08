package opencode

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

// sendViaPortal sends a pre-built message through bridgev2's QueueRemoteEvent pipeline.
func (oc *OpenCodeClient) sendViaPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	instanceID string,
	converted *bridgev2.ConvertedMessage,
) error {
	if portal == nil || portal.MXID == "" {
		return fmt.Errorf("invalid portal")
	}
	sender := oc.SenderForOpenCode(instanceID, false)
	msgID := newOpenCodeMessageID()
	evt := &OpenCodeRemoteMessage{
		Portal:    portal.PortalKey,
		ID:        msgID,
		Sender:    sender,
		Timestamp: time.Now(),
		LogKey:    "opencode_msg_id",
		PreBuilt:  converted,
	}
	result := oc.UserLogin.QueueRemoteEvent(evt)
	if !result.Success {
		if result.Error != nil {
			return fmt.Errorf("send failed: %w", result.Error)
		}
		return fmt.Errorf("send failed")
	}
	return nil
}

// sendSystemNoticeViaPortal is a convenience wrapper for sending MsgNotice via the pipeline.
func (oc *OpenCodeClient) sendSystemNoticeViaPortal(ctx context.Context, portal *bridgev2.Portal, msg string) {
	pmeta := oc.PortalMeta(portal)
	instanceID := ""
	if pmeta != nil {
		instanceID = pmeta.InstanceID
	}
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:   networkid.PartID("0"),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType:  event.MsgNotice,
				Body:     msg,
				Mentions: &event.Mentions{},
			},
		}},
	}
	if err := oc.sendViaPortal(ctx, portal, instanceID, converted); err != nil {
		oc.Log().Warn().Err(err).Msg("Failed to send system notice")
	}
}
