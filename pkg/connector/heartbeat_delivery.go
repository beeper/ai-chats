package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) resolveHeartbeatDeliveryTarget(agentID string, heartbeat *HeartbeatConfig, entry *sessionEntry) deliveryTarget {
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
		return oc.resolveHeartbeatDeliveryRoom(strings.TrimSpace(*heartbeat.To))
	}

	if heartbeat != nil && heartbeat.Target != nil {
		trimmed := strings.TrimSpace(*heartbeat.Target)
		if trimmed != "" && !strings.EqualFold(trimmed, "last") {
			return oc.resolveHeartbeatDeliveryRoom(trimmed)
		}
	}

	// Resolve from session entry's last route (channel-match validation: only use
	// lastTo when lastChannel is empty or "matrix", matching clawdbot's
	// resolveSessionDeliveryTarget channel===lastChannel guard).
	if entry != nil {
		lastChannel := strings.TrimSpace(entry.LastChannel)
		lastTo := strings.TrimSpace(entry.LastTo)
		if lastTo != "" && (lastChannel == "" || strings.EqualFold(lastChannel, "matrix")) {
			target := oc.resolveHeartbeatDeliveryRoom(lastTo)
			if target.Portal != nil && target.RoomID != "" {
				// Stale agent routing guard: skip if portal is now assigned to a
				// different agent (matches resolveHeartbeatSessionPortal behavior).
				if meta := portalMeta(target.Portal); meta != nil && normalizeAgentID(resolveAgentID(meta)) != normalizeAgentID(agentID) {
					// Fall through to lastActivePortal / defaultChatPortal.
				} else {
					return target
				}
			}
		}
	}

	// Fallback chain matching resolveHeartbeatSessionPortal and resolveCronDeliveryTarget:
	// lastActivePortal → defaultChatPortal.
	if portal := oc.lastActivePortal(agentID); portal != nil && portal.MXID != "" {
		return deliveryTarget{Portal: portal, RoomID: portal.MXID, Channel: "matrix", Reason: "last-active"}
	}
	if portal := oc.defaultChatPortal(); portal != nil && portal.MXID != "" {
		return deliveryTarget{Portal: portal, RoomID: portal.MXID, Channel: "matrix", Reason: "default-chat"}
	}

	return deliveryTarget{Reason: "no-target"}
}

func (oc *AIClient) resolveHeartbeatDeliveryRoom(raw string) deliveryTarget {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return deliveryTarget{Reason: "no-target"}
	}
	if !strings.HasPrefix(trimmed, "!") {
		return deliveryTarget{Reason: "no-target"}
	}
	portal := oc.portalByRoomID(context.Background(), id.RoomID(trimmed))
	if portal == nil || portal.MXID == "" {
		return deliveryTarget{Reason: "no-target"}
	}
	// Guard: don't deliver if the bridge isn't connected
	// (matches resolveCronDeliveryTarget's IsLoggedIn check).
	if !oc.IsLoggedIn() {
		return deliveryTarget{Channel: "matrix", Reason: "channel-not-ready"}
	}
	return deliveryTarget{
		Portal:  portal,
		RoomID:  portal.MXID,
		Channel: "matrix",
	}
}
