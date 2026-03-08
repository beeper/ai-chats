package cron

import (
	"strings"
)

type DeliveryTarget struct {
	Portal  any
	RoomID  string
	Channel string
	Reason  string
}

type DeliveryResolverDeps struct {
	ResolveLastTarget   func(agentID string) (channel string, target string, ok bool)
	IsStaleTarget       func(roomID string, agentID string) bool
	LastActiveRoomID    func(agentID string) string
	DefaultChatRoomID   func() string
	ResolvePortalByRoom func(roomID string) any
	IsLoggedIn          func() bool
}

func ResolveCronDeliveryTarget(agentID string, delivery *Delivery, deps DeliveryResolverDeps) DeliveryTarget {
	if delivery == nil {
		return DeliveryTarget{Reason: "no-delivery"}
	}

	channel := strings.TrimSpace(delivery.Channel)
	if channel == "" {
		channel = "last"
	}
	lowered := strings.ToLower(channel)
	if lowered != "last" && lowered != "matrix" {
		return DeliveryTarget{Channel: lowered, Reason: "unsupported-channel"}
	}

	target := strings.TrimSpace(delivery.To)
	if target == "" && lowered == "last" {
		if deps.ResolveLastTarget != nil {
			lastChannel, candidate, ok := deps.ResolveLastTarget(agentID)
			if ok {
				lastChannel = strings.TrimSpace(lastChannel)
				candidate = strings.TrimSpace(candidate)
				if (lastChannel == "" || strings.EqualFold(lastChannel, "matrix")) && candidate != "" {
					if strings.HasPrefix(candidate, "!") {
						if deps.IsStaleTarget != nil && deps.IsStaleTarget(candidate, agentID) {
							candidate = ""
						}
					}
					target = candidate
				}
			}
		}
		if target == "" && deps.LastActiveRoomID != nil {
			target = strings.TrimSpace(deps.LastActiveRoomID(agentID))
		}
		if target == "" && deps.DefaultChatRoomID != nil {
			target = strings.TrimSpace(deps.DefaultChatRoomID())
		}
	}

	if target == "" {
		return DeliveryTarget{Channel: "matrix", Reason: "no-target"}
	}
	if !strings.HasPrefix(target, "!") {
		return DeliveryTarget{Channel: "matrix", Reason: "invalid-target"}
	}
	if deps.ResolvePortalByRoom == nil {
		return DeliveryTarget{Channel: "matrix", Reason: "no-target"}
	}
	portal := deps.ResolvePortalByRoom(target)
	if portal == nil {
		return DeliveryTarget{Channel: "matrix", Reason: "no-target"}
	}
	if deps.IsLoggedIn != nil && !deps.IsLoggedIn() {
		return DeliveryTarget{Channel: "matrix", Reason: "channel-not-ready"}
	}
	return DeliveryTarget{Portal: portal, RoomID: target, Channel: "matrix"}
}
