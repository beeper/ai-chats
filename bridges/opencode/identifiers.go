package opencode

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

func makeOpenCodeUserLoginID(mxid id.UserID, ordinal int) networkid.UserLoginID {
	return bridgeadapter.MakeUserLoginID("opencode", mxid, ordinal)
}

func nextOpenCodeUserLoginID(user *bridgev2.User) networkid.UserLoginID {
	return bridgeadapter.NextUserLoginID(user, "opencode")
}
