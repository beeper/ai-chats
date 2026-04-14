package ai

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

type portalRoomMaterializeOptions struct {
	CleanupOnCreateError string
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
	return nil
}
