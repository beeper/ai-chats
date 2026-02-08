package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

func isMatrixBotUser(ctx context.Context, bridge *bridgev2.Bridge, userID id.UserID) bool {
	if userID == "" || bridge == nil {
		return false
	}
	if bridge.Bot != nil && bridge.Bot.GetMXID() == userID {
		return true
	}
	ghost, err := bridge.GetGhostByMXID(ctx, userID)
	return err == nil && ghost != nil
}
