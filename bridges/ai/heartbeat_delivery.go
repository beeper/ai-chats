package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) resolveHeartbeatDeliveryTarget(agentID string, heartbeat *HeartbeatConfig, sessionKey string) deliveryTarget {
	if oc == nil || oc.UserLogin == nil {
		return deliveryTarget{Reason: "no-target"}
	}
	// Guard: don't resolve a delivery target if the bridge isn't connected
	// (matches resolveCronDeliveryTarget's IsLoggedIn check).
	if !oc.IsLoggedIn() {
		return deliveryTarget{Channel: "matrix", Reason: "channel-not-ready"}
	}
	if heartbeat != nil && heartbeat.Target != nil {
		if strings.EqualFold(strings.TrimSpace(*heartbeat.Target), "none") {
			return deliveryTarget{Reason: "target-none"}
		}
	}

	if heartbeat != nil && heartbeat.To != nil && strings.TrimSpace(*heartbeat.To) != "" {
		return oc.heartbeatDeliveryTargetForRoom(agentID, strings.TrimSpace(*heartbeat.To), "")
	}

	if heartbeat != nil && heartbeat.Target != nil {
		trimmed := strings.TrimSpace(*heartbeat.Target)
		if trimmed != "" && !strings.EqualFold(trimmed, "last") {
			return oc.heartbeatDeliveryTargetForRoom(agentID, trimmed, "")
		}
	}

	if target := oc.heartbeatDeliveryTargetForRoom(agentID, sessionKey, ""); target.Portal != nil && target.RoomID != "" {
		return target
	}

	if portal, reason := oc.resolveHeartbeatFallbackPortal(agentID); portal != nil {
		return oc.heartbeatDeliveryTargetForPortal(portal, reason)
	}

	return deliveryTarget{Reason: "no-target"}
}

func (oc *AIClient) heartbeatPortalByRoom(agentID string, raw string) *bridgev2.Portal {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "!") {
		return nil
	}
	portal := oc.portalByRoomID(context.Background(), id.RoomID(trimmed))
	if portal == nil || portal.MXID == "" {
		return nil
	}
	if meta := portalMeta(portal); meta != nil && normalizeAgentID(resolveAgentID(meta)) != normalizeAgentID(agentID) {
		return nil
	}
	return portal
}

func (oc *AIClient) resolveHeartbeatFallbackPortal(agentID string) (*bridgev2.Portal, string) {
	if portal := oc.lastActivePortal(agentID); portal != nil && portal.MXID != "" {
		return portal, "last-active"
	}
	if portal := oc.defaultChatPortal(); portal != nil && portal.MXID != "" {
		return portal, "default-chat"
	}
	return nil, ""
}

func (oc *AIClient) heartbeatDeliveryTargetForRoom(agentID, raw, reason string) deliveryTarget {
	portal := oc.heartbeatPortalByRoom(agentID, raw)
	if portal == nil {
		return deliveryTarget{Reason: "no-target"}
	}
	return oc.heartbeatDeliveryTargetForPortal(portal, reason)
}

func (oc *AIClient) heartbeatDeliveryTargetForPortal(portal *bridgev2.Portal, reason string) deliveryTarget {
	if portal == nil || portal.MXID == "" {
		return deliveryTarget{Reason: "no-target"}
	}
	if !oc.IsLoggedIn() {
		return deliveryTarget{Channel: "matrix", Reason: "channel-not-ready"}
	}
	target := deliveryTarget{
		Portal:  portal,
		RoomID:  portal.MXID,
		Channel: "matrix",
	}
	if reason != "" {
		target.Reason = reason
	}
	return target
}
