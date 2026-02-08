package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

// portalByRoomID looks up a portal by its Matrix room ID, returning nil if
// the lookup fails or the portal does not exist.
func (oc *AIClient) portalByRoomID(ctx context.Context, roomID id.RoomID) *bridgev2.Portal {
	portal, err := oc.UserLogin.Bridge.GetPortalByMXID(ctx, roomID)
	if err != nil || portal == nil {
		return nil
	}
	return portal
}

func cleanupPortal(ctx context.Context, client *AIClient, portal *bridgev2.Portal, reason string) {
	if portal == nil || client == nil || client.UserLogin == nil {
		return
	}

	if portal.MXID != "" {
		if err := portal.Delete(ctx); err != nil {
			client.log.Warn().Err(err).
				Str("portal_id", string(portal.PortalKey.ID)).
				Str("mxid", string(portal.MXID)).
				Str("reason", reason).
				Msg("Failed to delete Matrix room during cleanup")
		}
	}

	if err := client.UserLogin.Bridge.DB.Portal.Delete(ctx, portal.PortalKey); err != nil {
		client.log.Warn().Err(err).
			Str("portal_id", string(portal.PortalKey.ID)).
			Str("reason", reason).
			Msg("Failed to delete portal during cleanup")
	}
}
