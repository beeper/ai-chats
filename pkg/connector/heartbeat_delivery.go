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

	if entry != nil {
		lastChannel := strings.TrimSpace(entry.LastChannel)
		lastTo := strings.TrimSpace(entry.LastTo)
		if lastTo != "" && (lastChannel == "" || strings.EqualFold(lastChannel, "matrix")) {
			target := oc.resolveHeartbeatDeliveryRoom(lastTo)
			if target.Portal != nil && target.RoomID != "" {
				return target
			}
		}
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
	portal, err := oc.UserLogin.Bridge.GetPortalByMXID(context.Background(), id.RoomID(trimmed))
	if err != nil || portal == nil || portal.MXID == "" {
		return deliveryTarget{Reason: "no-target"}
	}
	return deliveryTarget{
		Portal:  portal,
		RoomID:  portal.MXID,
		Channel: "matrix",
	}
}
