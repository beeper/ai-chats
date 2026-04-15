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

	storeAgentID := oc.sessionStoreAgentID(agentID)
	oc.touchStoredSession(ctx, storeAgentID, portal.MXID.String(), 0)
}

func (oc *AIClient) lastActivePortal(agentID string) *bridgev2.Portal {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	room, ok := oc.lastRoutedSessionKey(context.Background(), agentID)
	if !ok {
		return nil
	}
	room = strings.TrimSpace(room)
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
