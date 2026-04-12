package ai

import (
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

// newBrokenLoginClient creates a BrokenLoginClient that also wires up
// best-effort login data purge on logout.
func newBrokenLoginClient(login *bridgev2.UserLogin, reason string) *sdk.BrokenLoginClient {
	c := sdk.NewBrokenLoginClient(login, reason)
	c.OnLogout = purgeLoginDataBestEffort
	return c
}
