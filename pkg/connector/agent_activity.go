package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) recordAgentActivity(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) {
	if oc == nil || portal == nil || portal.MXID == "" || meta == nil {
		return
	}
	if meta.IsCronRoom {
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
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta == nil {
		return
	}
	if loginMeta.LastActiveRoomByAgent == nil {
		loginMeta.LastActiveRoomByAgent = make(map[string]string)
	}
	loginMeta.LastActiveRoomByAgent[agentID] = portal.MXID.String()
	_ = oc.UserLogin.Save(ctx)

	storeRef, mainKey := oc.resolveHeartbeatMainSessionRef(agentID)
	accountID := string(oc.UserLogin.ID)
	if mainKey != "" {
		oc.updateSessionEntry(ctx, storeRef, mainKey, func(entry sessionEntry) sessionEntry {
			patch := sessionEntry{
				LastChannel:   "matrix",
				LastTo:        portal.MXID.String(),
				LastAccountID: accountID,
			}
			return mergeSessionEntry(entry, patch)
		})
	}
	if portal.MXID.String() != mainKey {
		oc.updateSessionEntry(ctx, storeRef, portal.MXID.String(), func(entry sessionEntry) sessionEntry {
			patch := sessionEntry{
				LastChannel:   "matrix",
				LastTo:        portal.MXID.String(),
				LastAccountID: accountID,
			}
			return mergeSessionEntry(entry, patch)
		})
	}
}

func (oc *AIClient) lastActivePortal(agentID string) *bridgev2.Portal {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta == nil || loginMeta.LastActiveRoomByAgent == nil {
		return nil
	}
	room := loginMeta.LastActiveRoomByAgent[normalizeAgentID(agentID)]
	if room == "" {
		return nil
	}
	portal := oc.portalByRoomID(context.Background(), id.RoomID(room))
	// Guard against stale mappings when a room's agent assignment changes.
	if portal != nil {
		if meta := portalMeta(portal); meta != nil && normalizeAgentID(meta.AgentID) != normalizeAgentID(agentID) {
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
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta != nil && loginMeta.DefaultChatPortalID != "" {
		portalKey := networkid.PortalKey{
			ID:       networkid.PortalID(loginMeta.DefaultChatPortalID),
			Receiver: oc.UserLogin.ID,
		}
		if portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey); err == nil && portal != nil {
			return portal
		}
	}
	if portal, err := oc.UserLogin.Bridge.GetExistingPortalByKey(ctx, defaultChatPortalKey(oc.UserLogin.ID)); err == nil && portal != nil {
		return portal
	}
	return nil
}
