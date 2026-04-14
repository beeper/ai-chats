package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) recordAgentActivity(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) {
	if oc == nil || portal == nil || portal.MXID == "" || meta == nil {
		return
	}
	if meta.InternalRoom() {
		return
	}
	// Don't update last-route from heartbeat responses — heartbeat delivery
	// should read the route set by user activity, not overwrite it with its own
	// delivery target. Matches clawdbot where heartbeats don't call updateLastRoute.
	if heartbeatRunFromContext(ctx) != nil {
		return
	}
	agentID := normalizeAgentID(resolveAgentID(meta))
	if agentID == "" {
		return
	}

	storeRef, mainKey := oc.resolveHeartbeatMainSessionRef(agentID)
	if mainKey != "" {
		oc.updateSessionEntry(ctx, storeRef, mainKey, func(entry sessionEntry) sessionEntry {
			patch := sessionEntry{
				LastChannel: "matrix",
				LastTo:      portal.MXID.String(),
			}
			return mergeSessionEntry(entry, patch)
		})
	}
	if portal.MXID.String() != mainKey {
		oc.updateSessionEntry(ctx, storeRef, portal.MXID.String(), func(entry sessionEntry) sessionEntry {
			patch := sessionEntry{
				LastChannel: "matrix",
				LastTo:      portal.MXID.String(),
			}
			return mergeSessionEntry(entry, patch)
		})
	}
}

func (oc *AIClient) lastActivePortal(agentID string) *bridgev2.Portal {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	storeRef, mainKey := oc.resolveHeartbeatMainSessionRef(agentID)
	if mainKey == "" {
		return nil
	}
	entry, ok := oc.getSessionEntry(context.Background(), storeRef, mainKey)
	if !ok {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(entry.LastChannel), "matrix") && strings.TrimSpace(entry.LastChannel) != "" {
		return nil
	}
	room := strings.TrimSpace(entry.LastTo)
	if room == "" {
		return nil
	}
	portal := oc.portalByRoomID(context.Background(), id.RoomID(room))
	// Guard against stale mappings when a room's agent assignment changes.
	if portal != nil {
		if meta := portalMeta(portal); meta != nil && normalizeAgentID(resolveAgentID(meta)) != normalizeAgentID(agentID) {
			return nil
		}
	}
	return portal
}

func (oc *AIClient) defaultChatPortal() *bridgev2.Portal {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	ctx := oc.backgroundContext(context.Background())
	if portal, err := oc.UserLogin.Bridge.GetExistingPortalByKey(ctx, defaultChatPortalKey(oc.UserLogin.ID)); err == nil && portal != nil {
		return portal
	}
	if portals, err := oc.listAllChatPortals(ctx); err == nil {
		for _, portal := range portals {
			if portal == nil {
				continue
			}
			if shouldExcludeModelVisiblePortal(portalMeta(portal)) {
				continue
			}
			return portal
		}
	}
	return nil
}
