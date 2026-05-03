package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

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
		deleteAITurnsForPortal(ctx, portal)
	}
}
