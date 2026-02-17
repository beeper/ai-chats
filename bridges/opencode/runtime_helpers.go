package opencode

import (
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

type brokenLoginClient = bridgeadapter.BrokenLoginClient

func newBrokenLoginClient(login *bridgev2.UserLogin, reason string) *brokenLoginClient {
	return &brokenLoginClient{
		UserLogin: login,
		Reason:    reason,
	}
}
