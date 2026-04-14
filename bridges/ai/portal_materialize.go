package ai

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type portalRoomMaterializeOptions struct {
	SaveBefore           bool
	CleanupOnCreateError string
	MutatePortal         func(*bridgev2.Portal)
}

type portalRoomResolveOptions struct {
	SkipIfExists bool
	Materialize  portalRoomMaterializeOptions
}

func (oc *AIClient) getOrMaterializePortalRoom(
	ctx context.Context,
	portalKey networkid.PortalKey,
	chatInfo *bridgev2.ChatInfo,
	opts portalRoomResolveOptions,
) (*bridgev2.Portal, error) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return nil, fmt.Errorf("AIClient not initialized: missing bridge")
	}
	portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, err
	}
	if opts.SkipIfExists && portal.MXID != "" {
		return portal, nil
	}
	if err := oc.materializePortalRoom(ctx, portal, chatInfo, opts.Materialize); err != nil {
		return nil, err
	}
	return portal, nil
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
	return nil
}
