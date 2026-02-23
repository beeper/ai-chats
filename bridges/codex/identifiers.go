package codex

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/rs/xid"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

func makeCodexUserLoginID(mxid id.UserID, instanceID string) networkid.UserLoginID {
	escaped := url.PathEscape(string(mxid))
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		instanceID = xid.New().String()
	}
	return networkid.UserLoginID(fmt.Sprintf("codex:%s:%s", escaped, instanceID))
}

// MakeMessageID creates a message ID from a Matrix event ID.
func MakeMessageID(eventID id.EventID) networkid.MessageID {
	return bridgeadapter.MatrixMessageID(eventID)
}

func generateShortID() string {
	return xid.New().String()
}
