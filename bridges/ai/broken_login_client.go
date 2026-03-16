package ai

import (
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
)

// newBrokenLoginClient creates a BrokenLoginClient that also wires up
// best-effort login data purge on logout.
func newBrokenLoginClient(login *bridgev2.UserLogin, reason string) *agentremote.BrokenLoginClient {
	c := agentremote.NewBrokenLoginClient(login, reason)
	c.OnLogout = purgeLoginDataBestEffort
	return c
}
