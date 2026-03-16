package opencode

import (
	"context"
)

func (b *Bridge) EnsureGhostDisplayName(ctx context.Context, instanceID string) {
	if b == nil || b.host == nil {
		return
	}
	login := b.host.GetUserLogin()
	if login == nil || login.Bridge == nil {
		return
	}
	ghost, err := login.Bridge.GetGhostByID(ctx, OpenCodeUserID(instanceID))
	if err != nil || ghost == nil {
		return
	}
	displayName := b.DisplayName(instanceID)
	needsUpdate := ghost.Name == "" || !ghost.NameSet || ghost.Name != displayName || !ghost.IsBot
	if needsUpdate {
		ghost.UpdateInfo(ctx, openCodeSDKAgent(instanceID, displayName).UserInfo())
	}
}
