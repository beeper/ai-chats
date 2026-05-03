package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
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
	oc.sendAIChatsRoomInfo(ctx, params.Portal)
	return params.Portal, nil
}

func (oc *AIClient) sendAIChatsRoomInfo(ctx context.Context, portal *bridgev2.Portal) bool {
	if portal == nil || portal.MXID == "" || portal.Bridge == nil || portal.Bridge.Bot == nil {
		return false
	}
	aiKind := aiPortalKind(portalMeta(portal))
	if aiKind == "" {
		aiKind = "chat"
	}
	_, err := portal.Bridge.Bot.SendState(ctx, portal.MXID, AIRoomInfoEventType, "", &event.Content{
		Parsed: &AIRoomInfoContent{Type: aiKind},
		Raw:    map[string]any{"com.beeper.exclude_from_timeline": true},
	}, time.Now())
	if err != nil {
		logger := zerolog.Ctx(ctx)
		if logger == nil || logger.GetLevel() == zerolog.Disabled {
			logger = oc.loggerForContext(ctx)
		}
		logger.Warn().Err(err).Stringer("room_id", portal.MXID).Msg("Failed to send AI room info state event")
		return false
	}
	return true
}
