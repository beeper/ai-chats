package ai

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
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
	if opts.SaveBefore {
		if err := portal.Save(ctx); err != nil {
			return fmt.Errorf("failed to save portal: %w", err)
		}
	}
	if err := portal.CreateMatrixRoom(ctx, oc.UserLogin, chatInfo); err != nil {
		if opts.CleanupOnCreateError != "" {
			cleanupPortal(ctx, oc, portal, opts.CleanupOnCreateError)
		}
		return err
	}
	agentremote.SendAIRoomInfo(ctx, portal, integrationPortalAIKind(portalMeta(portal)))
	if opts.SendWelcome {
		oc.sendWelcomeMessage(ctx, portal)
	}
	return nil
}
