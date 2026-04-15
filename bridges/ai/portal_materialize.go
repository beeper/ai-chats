package ai

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

type ensurePortalRoomParams struct {
	Portal   *bridgev2.Portal
	ChatInfo *bridgev2.ChatInfo
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

	created := params.Portal.MXID == ""
	if created {
		if err := params.Portal.CreateMatrixRoom(ctx, oc.UserLogin, chatInfo); err != nil {
			cleanupPortal(ctx, oc, params.Portal, "failed to create Matrix room")
			return nil, err
		}
	} else if chatInfo != nil {
		params.Portal.UpdateInfo(ctx, chatInfo, oc.UserLogin, nil, time.Time{})
	}
	params.Portal.UpdateBridgeInfo(ctx)
	params.Portal.UpdateCapabilities(ctx, oc.UserLogin, true)
	return params.Portal, nil
}
