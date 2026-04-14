package ai

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

type portalRoomMaterializeOptions struct {
	SaveBefore           bool
	CleanupOnCreateError string
	SendWelcome          bool
	MutatePortal         func(*bridgev2.Portal)
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
	if opts.MutatePortal != nil {
		opts.MutatePortal(portal)
	}
	if opts.SaveBefore {
		if err := portal.Save(ctx); err != nil {
			return fmt.Errorf("failed to save portal: %w", err)
		}
	}
	created := portal.MXID == ""
	if created {
		if err := portal.CreateMatrixRoom(ctx, oc.UserLogin, chatInfo); err != nil {
			if opts.CleanupOnCreateError != "" {
				cleanupPortal(ctx, oc, portal, opts.CleanupOnCreateError)
			}
			return err
		}
	} else if chatInfo != nil {
		portal.UpdateInfo(ctx, chatInfo, oc.UserLogin, nil, time.Time{})
	}
	portal.UpdateBridgeInfo(ctx)
	portal.UpdateCapabilities(ctx, oc.UserLogin, true)
	if created {
		if opts.SendWelcome {
			if err := oc.sendWelcomeMessage(ctx, portal); err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to send welcome message")
			}
		}
	}
	return nil
}
