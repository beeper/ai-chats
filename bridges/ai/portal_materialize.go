package ai

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

type portalRoomMaterializeOptions struct {
	SaveBefore           bool
	CleanupOnCreateError string
	SendWelcome          bool
}

func (oc *AIClient) materializePortalRoom(
	ctx context.Context,
	portal *bridgev2.Portal,
	chatInfo *bridgev2.ChatInfo,
	opts portalRoomMaterializeOptions,
) error {
	if portal == nil {
		return fmt.Errorf("missing portal")
	}
	if oc == nil || oc.UserLogin == nil {
		return fmt.Errorf("AIClient not initialized: missing UserLogin")
	}
	created, err := sdk.EnsurePortalLifecycle(ctx, sdk.PortalLifecycleOptions{
		Login:            oc.UserLogin,
		Portal:           portal,
		ChatInfo:         chatInfo,
		SaveBeforeCreate: opts.SaveBefore,
		CleanupOnCreateError: func(ctx context.Context, portal *bridgev2.Portal) {
			if opts.CleanupOnCreateError != "" {
				cleanupPortal(ctx, oc, portal, opts.CleanupOnCreateError)
			}
		},
		AIRoomKind:        integrationPortalAIKind(portalMeta(portal)),
		ForceCapabilities: true,
	})
	if err != nil {
		return err
	}
	if created && opts.SendWelcome {
		oc.sendWelcomeMessage(ctx, portal)
	}
	return nil
}
