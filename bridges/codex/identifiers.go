package codex

import (
	"strings"

	"github.com/rs/xid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

func makeCodexUserLoginID(mxid id.UserID, ordinal int) networkid.UserLoginID {
	return bridgeadapter.MakeUserLoginID("codex", mxid, ordinal)
}

func nextCodexUserLoginID(user *bridgev2.User) networkid.UserLoginID {
	return bridgeadapter.NextUserLoginID(user, "codex")
}

func generateShortID() string {
	return xid.New().String()
}

func isCodexIdentifier(identifier string) bool {
	switch strings.ToLower(strings.TrimSpace(identifier)) {
	case "codex", "@codex", "codex:default", "codex:codex":
		return true
	default:
		return false
	}
}
