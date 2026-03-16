package codex

import (
	"fmt"
	"net/url"
	"strings"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

func codexWelcomePortalKey(loginID networkid.UserLoginID, slug string) (networkid.PortalKey, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return networkid.PortalKey{}, fmt.Errorf("empty welcome slug")
	}
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("codex:%s:welcome:%s", loginID, url.PathEscape(slug))),
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
