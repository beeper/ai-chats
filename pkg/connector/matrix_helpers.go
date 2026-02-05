package connector

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) matrixRoomDisplayName(ctx context.Context, portal *bridgev2.Portal) string {
	if portal == nil || oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Matrix == nil {
		if portal != nil {
			return portal.MXID.String()
		}
		return ""
	}
	if info, err := getMatrixRoomInfo(ctx, &BridgeToolContext{Client: oc, Portal: portal}); err == nil && info != nil {
		if info.Name != "" {
			return info.Name
		}
	}
	name := portalRoomName(portal)
	if name != "" {
		return name
	}
	return portal.MXID.String()
}

func (oc *AIClient) lastPortalMessageTime(ctx context.Context, portal *bridgev2.Portal) (time.Time, bool) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || portal == nil {
		return time.Time{}, false
	}
	history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 1)
	if err != nil || len(history) == 0 {
		return time.Time{}, false
	}
	return history[0].Timestamp, true
}

func (oc *AIClient) resolveBotMXID(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) id.UserID {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return ""
	}
	if portal != nil && portal.OtherUserID != "" {
		if ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, portal.OtherUserID); err == nil && ghost != nil {
			return ghost.Intent.GetMXID()
		}
	}
	modelID := oc.effectiveModel(meta)
	if modelID != "" {
		if ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, modelUserID(modelID)); err == nil && ghost != nil {
			return ghost.Intent.GetMXID()
		}
	}
	return ""
}

func (oc *AIClient) buildMatrixInboundBody(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	evt *event.Event,
	rawBody string,
	senderName string,
	roomName string,
	isGroup bool,
) string {
	body := strings.TrimSpace(rawBody)
	if body == "" {
		return ""
	}
	if evt != nil && evt.ID != "" && portal != nil && portal.MXID != "" {
		body = body + "\n[matrix event id: " + evt.ID.String() + " room: " + portal.MXID.String() + "]"
	}
	from := senderName
	if isGroup {
		if roomName != "" {
			from = roomName
		} else if portal != nil {
			from = portal.MXID.String()
		}
	}
	opts := oc.resolveEnvelopeFormatOptions()
	timestamp := time.Time{}
	hasTimestamp := false
	if evt != nil && evt.Timestamp > 0 {
		timestamp = time.UnixMilli(evt.Timestamp)
		hasTimestamp = true
	}
	prev, hasPrev := oc.lastPortalMessageTime(ctx, portal)
	enveloped := formatAgentEnvelope(struct {
		Channel         string
		From            string
		Body            string
		Timestamp       time.Time
		HasTimestamp    bool
		PreviousTime    time.Time
		HasPreviousTime bool
		Envelope        EnvelopeFormatOptions
	}{
		Channel:         "Matrix",
		From:            from,
		Body:            body,
		Timestamp:       timestamp,
		HasTimestamp:    hasTimestamp,
		PreviousTime:    prev,
		HasPreviousTime: hasPrev,
		Envelope:        opts,
	})
	return formatInboundBodyWithSenderMeta(enveloped, senderName, isGroup)
}
