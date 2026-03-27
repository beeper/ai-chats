package codex

import (
	"fmt"
	"net/url"
	"strings"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

func codexWelcomePortalKey(loginID networkid.UserLoginID) (networkid.PortalKey, error) {
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("codex:%s:welcome", loginID)),
		Receiver: loginID,
	}, nil
}

func codexThreadPortalKey(loginID networkid.UserLoginID, threadID string) (networkid.PortalKey, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return networkid.PortalKey{}, fmt.Errorf("empty threadID")
	}
	return networkid.PortalKey{
		ID: networkid.PortalID(
			fmt.Sprintf(
				"codex:%s:thread:%s",
				loginID,
				url.PathEscape(threadID),
			),
		),
		Receiver: loginID,
	}, nil
}

func codexWorkspacePortalKey(loginID networkid.UserLoginID, root string) (networkid.PortalKey, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return networkid.PortalKey{}, fmt.Errorf("empty workspace root")
	}
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("codex:%s:workspace:%s", loginID, url.PathEscape(root))),
		Receiver: loginID,
	}, nil
}
