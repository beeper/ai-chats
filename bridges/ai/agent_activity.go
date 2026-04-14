package ai

import (
	"context"
	"database/sql"
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

	storeAgentID := oc.resolveSessionRouting(agentID).StoreAgentID
	oc.updateSessionTimestamp(ctx, storeAgentID, portal.MXID.String(), 0)
}

func (oc *AIClient) lastRoute(agentID string) (channel string, target string, ok bool) {
	if oc == nil {
		return "", "", false
	}
	scope := loginScopeForClient(oc)
	if scope == nil {
		return "", "", false
	}
	routing := oc.resolveSessionRouting(agentID)
	var sessionKey string
	err := scope.db.QueryRow(context.Background(), `
		SELECT session_key
		FROM `+aiSessionsTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND store_agent_id=$3 AND session_key<>$4 AND session_key LIKE '!%'
		ORDER BY updated_at_ms DESC
		LIMIT 1
	`, scope.bridgeID, scope.loginID, normalizeAgentID(routing.StoreAgentID), strings.TrimSpace(routing.MainKey)).Scan(&sessionKey)
	if err == sql.ErrNoRows {
		return "", "", false
	}
	if err != nil {
		oc.log.Warn().Err(err).Str("agent_id", agentID).Msg("session store: latest route lookup failed")
		return "", "", false
	}
	return "matrix", sessionKey, true
}

func (oc *AIClient) lastActiveRoomID(agentID string) string {
	channel, room, ok := oc.lastRoute(agentID)
	if !ok {
		return ""
	}
	channel = strings.TrimSpace(channel)
	room = strings.TrimSpace(room)
	if room == "" || (!strings.EqualFold(channel, "matrix") && channel != "") {
		return ""
	}
	return room
}

func (oc *AIClient) lastActivePortal(agentID string) *bridgev2.Portal {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	room := oc.lastActiveRoomID(agentID)
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
