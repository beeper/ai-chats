package ai

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

func (oc *AIClient) portalRoomInfo(ctx context.Context, portal *bridgev2.Portal) *bridgev2.ChatInfo {
	if portal == nil {
		return nil
	}
	meta := portalMeta(portal)
	if meta != nil && meta.InternalRoom() {
		fallbackName := strings.TrimSpace(meta.Slug)
		if fallbackName == "" {
			fallbackName = "AI Chat"
		}
		return bridgeutil.BuildPortalFallbackChatInfo(portal, fallbackName)
	}
	return oc.chatInfoFromPortal(ctx, portal)
}

// applyPortalRoomName updates the visible room name via bridgev2 for existing
// rooms and falls back to local portal fields before the room exists.
func (oc *AIClient) applyPortalRoomName(ctx context.Context, portal *bridgev2.Portal, name string) {
	if portal == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if portal.MXID != "" && oc != nil && oc.UserLogin != nil {
		portal.UpdateInfo(ctx, &bridgev2.ChatInfo{
			Name:                       &name,
			ExcludeChangesFromTimeline: true,
		}, oc.UserLogin, nil, time.Time{})
		return
	}
	portal.Name = name
	portal.NameSet = true
}
