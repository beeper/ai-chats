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

type portalRoomBootstrapParams struct {
	Portal               *bridgev2.Portal
	ChatInfo             *bridgev2.ChatInfo
	SaveAction           string
	Mutate               func(portal *bridgev2.Portal, chatInfo *bridgev2.ChatInfo)
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

func (oc *AIClient) bootstrapPortalRoom(
	ctx context.Context,
	params portalRoomBootstrapParams,
) (*bridgev2.Portal, error) {
	if params.Portal == nil {
		return nil, fmt.Errorf("missing portal")
	}
	if params.Mutate != nil {
		params.Mutate(params.Portal, params.ChatInfo)
	}
	if params.SaveAction != "" {
		if err := oc.savePortal(ctx, params.Portal, params.SaveAction); err != nil {
			return nil, err
		}
	}
	chatInfo := params.ChatInfo
	if chatInfo == nil {
		chatInfo = oc.chatInfoFromPortal(ctx, params.Portal)
	}
	if err := oc.materializePortalRoom(ctx, params.Portal, chatInfo, portalRoomMaterializeOptions{
		CleanupOnCreateError: params.CleanupOnCreateError,
	}); err != nil {
		return nil, err
	}
	return params.Portal, nil
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
	if err := bridgeutil.ConfigureAndPersistDMPortal(ctx, bridgeutil.ConfigureAndPersistDMPortalParams{
		Portal:      portal,
		Title:       displayName,
		OtherUserID: portal.OtherUserID,
		MutatePortal: func(portal *bridgev2.Portal) {
			meta := portalMeta(portal)
			if mutate != nil {
				mutate(portal, meta)
			}
		},
		Persist: func(ctx context.Context, portal *bridgev2.Portal) error {
			return oc.savePortal(ctx, portal, "named room setup")
		},
	}); err != nil {
		return nil, err
	}
	return oc.bootstrapPortalRoom(ctx, portalRoomBootstrapParams{
		Portal:               portal,
		CleanupOnCreateError: opts.CleanupOnCreateError,
	})
}
