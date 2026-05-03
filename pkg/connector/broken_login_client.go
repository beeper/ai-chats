package connector

import (
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-chats/pkg/shared/aihelpers"
)

// newBrokenLoginClient creates a BrokenLoginClient that also wires up
// best-effort login data purge on logout.
func newBrokenLoginClient(login *bridgev2.UserLogin, reason string) *aihelpers.BrokenLoginClient {
	c := &aihelpers.BrokenLoginClient{UserLogin: login, Reason: reason}
	c.OnLogout = purgeLoginData
	return c
}
