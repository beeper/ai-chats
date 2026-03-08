package codex

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/rs/xid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func makeCodexUserLoginID(mxid id.UserID, ordinal int) networkid.UserLoginID {
	escaped := url.PathEscape(string(mxid))
	base := networkid.UserLoginID(fmt.Sprintf("codex:%s", escaped))
	if ordinal <= 1 {
		return base
	}
	return networkid.UserLoginID(fmt.Sprintf("%s:%d", base, ordinal))
}

func nextCodexUserLoginID(user *bridgev2.User) networkid.UserLoginID {
	used := map[string]struct{}{}
	for _, existing := range user.GetUserLogins() {
		if existing == nil {
			continue
		}
		used[string(existing.ID)] = struct{}{}
	}
	for ordinal := 1; ; ordinal++ {
		loginID := makeCodexUserLoginID(user.MXID, ordinal)
		if _, ok := used[string(loginID)]; !ok {
			return loginID
		}
	}
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
