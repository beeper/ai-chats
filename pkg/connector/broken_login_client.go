package connector

import (
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

// brokenLoginClient is used when a stored login can't be fully initialized (e.g. missing credentials
// or invalid config). bridgev2 won't cache logins if LoadUserLogin returns an error, which makes them
// impossible to delete via provisioning. This client keeps the login loadable and deletable.
type brokenLoginClient = bridgeadapter.BrokenLoginClient

func newBrokenLoginClient(login *bridgev2.UserLogin, reason string) *brokenLoginClient {
	return &brokenLoginClient{
		UserLogin: login,
		Reason:    reason,
		OnLogout:  purgeLoginDataBestEffort,
	}
}
