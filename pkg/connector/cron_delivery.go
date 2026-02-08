package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/cron"
)

func (oc *AIClient) resolveCronDeliveryTarget(agentID string, delivery *cron.CronDelivery) deliveryTarget {
	if delivery == nil {
		return deliveryTarget{Reason: "no-delivery"}
	}

	channel := strings.TrimSpace(delivery.Channel)
	if channel == "" {
		channel = "last"
	}
	lowered := strings.ToLower(channel)
	if lowered != "last" && lowered != "matrix" {
		return deliveryTarget{Channel: lowered, Reason: "unsupported-channel"}
	}

	target := strings.TrimSpace(delivery.To)
	if target == "" && lowered == "last" {
		storeRef, mainKey := oc.resolveHeartbeatMainSessionRef(agentID)
		if entry, ok := oc.getSessionEntry(context.Background(), storeRef, mainKey); ok {
			lastChannel := strings.TrimSpace(entry.LastChannel)
			if lastChannel == "" || strings.EqualFold(lastChannel, "matrix") {
				target = strings.TrimSpace(entry.LastTo)
			}
		}
		if target == "" {
			if portal := oc.lastActivePortal(agentID); portal != nil && portal.MXID != "" {
				target = portal.MXID.String()
			}
		}
		if target == "" {
			if portal := oc.defaultChatPortal(); portal != nil && portal.MXID != "" {
				target = portal.MXID.String()
			}
		}
	}
	if target == "" {
		return deliveryTarget{Channel: "matrix", Reason: "no-target"}
	}
	if !strings.HasPrefix(target, "!") {
		return deliveryTarget{Channel: "matrix", Reason: "invalid-target"}
	}
	portal := oc.portalByRoomID(context.Background(), id.RoomID(target))
	if portal == nil {
		return deliveryTarget{Channel: "matrix", Reason: "no-target"}
	}
	if !oc.IsLoggedIn() {
		return deliveryTarget{Channel: "matrix", Reason: "channel-not-ready"}
	}
	return deliveryTarget{Portal: portal, RoomID: portal.MXID, Channel: "matrix"}
}
