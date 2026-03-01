package codex

import (
	"fmt"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

func defaultCodexChatPortalKey(loginID networkid.UserLoginID) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("codex:%s:default-chat", loginID)),
		Receiver: loginID,
	}
}
