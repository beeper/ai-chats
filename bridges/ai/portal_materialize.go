package ai

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
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
	if _, err := bridgeutil.MaterializePortalRoom(ctx, bridgeutil.MaterializePortalRoomParams{
		Login:    oc.UserLogin,
		Portal:   portal,
		ChatInfo: chatInfo,
	}); err != nil {
		if opts.CleanupOnCreateError != "" && portal.MXID == "" {
			cleanupPortal(ctx, oc, portal, opts.CleanupOnCreateError)
		}
		return err
	}
	return nil
}

func (oc *AIClient) ensureNamedPortalRoom(
	ctx context.Context,
	portalKey networkid.PortalKey,
	displayName string,
	mutate func(portal *bridgev2.Portal, meta *PortalMetadata),
	opts portalRoomMaterializeOptions,
) (*bridgev2.Portal, error) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return nil, fmt.Errorf("missing login")
	}
	portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, err
	}
	meta := portalMeta(portal)
	if meta == nil {
		meta = &PortalMetadata{}
		portal.Metadata = meta
	}
	if mutate != nil {
		mutate(portal, meta)
	}
	if displayName != "" {
		oc.applyPortalRoomName(ctx, portal, displayName)
	}
	if err := portal.Save(ctx); err != nil {
		return nil, err
	}
	chatInfo := oc.portalRoomInfo(ctx, portal)
	if err := oc.materializePortalRoom(ctx, portal, chatInfo, opts); err != nil {
		return nil, err
	}
	return portal, nil
}
