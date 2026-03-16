package agentremote

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func MatrixMessageID(eventID id.EventID) networkid.MessageID {
	return networkid.MessageID("mx:" + string(eventID))
}

// NewEventID generates a unique Matrix-style event ID with the given prefix.
func NewEventID(prefix string) id.EventID {
	return id.EventID(fmt.Sprintf("$%s-%s", prefix, uuid.NewString()))
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
	for ordinal := 1; ordinal <= len(used)+1; ordinal++ {
		loginID := MakeUserLoginID(prefix, user.MXID, ordinal)
		if _, ok := used[string(loginID)]; !ok {
			return loginID
		}
	}
	// Should be unreachable: there are at most len(used) occupied ordinals,
	// so ordinal len(used)+1 must be free. Fall back to a safe default.
	return MakeUserLoginID(prefix, user.MXID, len(used)+1)
}

// NewTurnID generates a new unique, sortable turn ID using a timestamp-based format.
func NewTurnID() string {
	return "turn_" + strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000000"), ".", "")
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
