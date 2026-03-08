package bridgeadapter

import (
	"fmt"
	"net/url"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func MatrixMessageID(eventID id.EventID) networkid.MessageID {
	return networkid.MessageID("mx:" + string(eventID))
}

func HumanUserID(prefix string, loginID networkid.UserLoginID) networkid.UserID {
	return networkid.UserID(prefix + ":" + string(loginID))
}

// MakeUserLoginID creates a login ID in the format "prefix:escaped-mxid[:ordinal]".
func MakeUserLoginID(prefix string, mxid id.UserID, ordinal int) networkid.UserLoginID {
	escaped := url.PathEscape(string(mxid))
	base := networkid.UserLoginID(fmt.Sprintf("%s:%s", prefix, escaped))
	if ordinal <= 1 {
		return base
	}
	return networkid.UserLoginID(fmt.Sprintf("%s:%d", base, ordinal))
}

// NextUserLoginID finds the next available ordinal for a login ID with the given prefix.
func NextUserLoginID(user *bridgev2.User, prefix string) networkid.UserLoginID {
	used := map[string]struct{}{}
	for _, existing := range user.GetUserLogins() {
		if existing == nil {
			continue
		}
		used[string(existing.ID)] = struct{}{}
	}
	for ordinal := 1; ; ordinal++ {
		loginID := MakeUserLoginID(prefix, user.MXID, ordinal)
		if _, ok := used[string(loginID)]; !ok {
			return loginID
		}
	}
}

func SingleLoginFlow(enabled bool, flow bridgev2.LoginFlow) []bridgev2.LoginFlow {
	if !enabled {
		return nil
	}
	return []bridgev2.LoginFlow{flow}
}

func ValidateSingleLoginFlow(flowID, expectedFlowID string, enabled bool) error {
	if flowID != expectedFlowID || !enabled {
		return fmt.Errorf("login flow %s is not available", flowID)
	}
	return nil
}
