package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
)

func (oc *AIClient) resolveCronDeliveryTarget(agentID string, delivery *integrationcron.Delivery) deliveryTarget {
	resolved := integrationcron.ResolveCronDeliveryTarget(agentID, delivery, integrationcron.DeliveryResolverDeps{
		ResolveLastTarget: func(agentID string) (string, string, bool) {
			storeRef, mainKey := oc.resolveHeartbeatMainSessionRef(agentID)
			entry, ok := oc.getSessionEntry(context.Background(), storeRef, mainKey)
			if !ok {
				return "", "", false
			}
			return entry.LastChannel, entry.LastTo, true
		},
		IsStaleTarget: func(roomID, agentID string) bool {
			candidate := strings.TrimSpace(roomID)
			if candidate == "" || !strings.HasPrefix(candidate, "!") {
				return false
			}
			if p := oc.portalByRoomID(context.Background(), id.RoomID(candidate)); p != nil {
				if meta := portalMeta(p); meta != nil && normalizeAgentID(meta.AgentID) != normalizeAgentID(agentID) {
					return true
				}
			}
			return false
		},
		LastActiveRoomID: func(agentID string) string {
			if portal := oc.lastActivePortal(agentID); portal != nil && portal.MXID != "" {
				return portal.MXID.String()
			}
			return ""
		},
		DefaultChatRoomID: func() string {
			if portal := oc.defaultChatPortal(); portal != nil && portal.MXID != "" {
				return portal.MXID.String()
			}
			return ""
		},
		ResolvePortalByRoom: func(roomID string) any {
			return oc.portalByRoomID(context.Background(), id.RoomID(roomID))
		},
		IsLoggedIn: oc.IsLoggedIn,
	})
	out := deliveryTarget{Channel: resolved.Channel, Reason: resolved.Reason}
	if portal, ok := resolved.Portal.(*bridgev2.Portal); ok && portal != nil {
		out.Portal = portal
		out.RoomID = portal.MXID
	}
	return out
}
