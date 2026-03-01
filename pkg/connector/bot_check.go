package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

func isMatrixBotUser(ctx context.Context, bridge *bridgev2.Bridge, userID id.UserID) bool {
	return bridgeadapter.IsMatrixBotUser(ctx, bridge, userID)
}
