package ai

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

type ensurePortalRoomParams struct {
	Portal               *bridgev2.Portal
	ChatInfo             *bridgev2.ChatInfo
	SaveAction           string
	Mutate               func(portal *bridgev2.Portal, chatInfo *bridgev2.ChatInfo)
	CleanupOnCreateError string
}

func (oc *AIClient) ensurePortalRoom(ctx context.Context, params ensurePortalRoomParams) (*bridgev2.Portal, error) {
	if params.Portal == nil {
		return nil, fmt.Errorf("missing portal")
	}
	if oc == nil || oc.UserLogin == nil {
		return nil, fmt.Errorf("AIClient not initialized: missing UserLogin")
	}

	chatInfo := params.ChatInfo
	if chatInfo == nil {
		chatInfo = oc.chatInfoFromPortal(ctx, params.Portal)
	}
	if params.Mutate != nil {
		params.Mutate(params.Portal, chatInfo)
	}
	if params.SaveAction != "" {
		if err := oc.savePortal(ctx, params.Portal, params.SaveAction); err != nil {
			return nil, err
		}
	}

	created := params.Portal.MXID == ""
	if created {
		if err := params.Portal.CreateMatrixRoom(ctx, oc.UserLogin, chatInfo); err != nil {
			if params.CleanupOnCreateError != "" {
				cleanupPortal(ctx, oc, params.Portal, params.CleanupOnCreateError)
			}
			return nil, err
		}
	} else if chatInfo != nil {
		params.Portal.UpdateInfo(ctx, chatInfo, oc.UserLogin, nil, time.Time{})
	}
	params.Portal.UpdateBridgeInfo(ctx)
	params.Portal.UpdateCapabilities(ctx, oc.UserLogin, true)
	return params.Portal, nil
}
