package opencodebridge

import (
	"context"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
)

func (b *Bridge) ensureOpenCodeGhostDisplayName(ctx context.Context, instanceID string) {
	if b == nil || b.host == nil {
		return
	}
	login := b.host.Login()
	if login == nil || login.Bridge == nil {
		return
	}
	ghost, err := login.Bridge.GetGhostByID(ctx, OpenCodeUserID(instanceID))
	if err != nil || ghost == nil {
		return
	}
	displayName := b.opencodeDisplayName(instanceID)
	if ghost.Name == "" || !ghost.NameSet || ghost.Name != displayName {
		ghost.UpdateInfo(ctx, &bridgev2.UserInfo{
			Name:  ptr.Ptr(displayName),
			IsBot: ptr.Ptr(true),
		})
	}
}
