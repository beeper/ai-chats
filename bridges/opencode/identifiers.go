package opencode

import (
	"fmt"
	"net/url"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func makeOpenCodeUserLoginID(mxid id.UserID, ordinal int) networkid.UserLoginID {
	escaped := url.PathEscape(string(mxid))
	base := networkid.UserLoginID(fmt.Sprintf("opencode:%s", escaped))
	if ordinal <= 1 {
		return base
	}
	return networkid.UserLoginID(fmt.Sprintf("%s:%d", base, ordinal))
}

func nextOpenCodeUserLoginID(user *bridgev2.User) networkid.UserLoginID {
	used := map[string]struct{}{}
	for _, existing := range user.GetUserLogins() {
		if existing == nil {
			continue
		}
		used[string(existing.ID)] = struct{}{}
	}
	for ordinal := 1; ; ordinal++ {
		loginID := makeOpenCodeUserLoginID(user.MXID, ordinal)
		if _, ok := used[string(loginID)]; !ok {
			return loginID
		}
	}
}
