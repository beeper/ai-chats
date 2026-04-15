package ai

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

type portalRoomMaterializeOptions struct {
	CleanupOnCreateError string
}

type ensurePortalRoomParams struct {
	Portal               *bridgev2.Portal
	ChatInfo             *bridgev2.ChatInfo
	SaveAction           string
	Mutate               func(portal *bridgev2.Portal, chatInfo *bridgev2.ChatInfo)
	CleanupOnCreateError string
}

func (oc *AIClient) syncPortalRoom(
	ctx context.Context,
	portal *bridgev2.Portal,
	chatInfo *bridgev2.ChatInfo,
	opts portalRoomMaterializeOptions,
) (bool, error) {
	if portal == nil {
		return false, fmt.Errorf("missing portal")
	}
	if oc == nil || oc.UserLogin == nil {
		return false, fmt.Errorf("AIClient not initialized: missing UserLogin")
	}
	created, err := bridgeutil.MaterializePortalRoom(ctx, bridgeutil.MaterializePortalRoomParams{
		Login:    oc.UserLogin,
		Portal:   portal,
		ChatInfo: chatInfo,
	})
	if err != nil {
		if opts.CleanupOnCreateError != "" && portal.MXID == "" {
			cleanupPortal(ctx, oc, portal, opts.CleanupOnCreateError)
		}
		return false, err
	}
	return created, nil
}

func (oc *AIClient) ensurePortalRoom(
	ctx context.Context,
	params ensurePortalRoomParams,
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
	if _, err := oc.syncPortalRoom(ctx, params.Portal, chatInfo, portalRoomMaterializeOptions{
		CleanupOnCreateError: params.CleanupOnCreateError,
	}); err != nil {
		return nil, err
	}
	return params.Portal, nil
}
