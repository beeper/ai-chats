package codex

import (
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

// CodexRemoteMessage is a type alias for the shared RemoteMessage.
type CodexRemoteMessage = bridgeadapter.RemoteMessage

// CodexRemoteEdit is a type alias for the shared RemoteEdit.
type CodexRemoteEdit = bridgeadapter.RemoteEdit

// newMessageID generates a unique message ID for Codex remote events.
func newMessageID() networkid.MessageID {
	return bridgeadapter.NewMessageID("codex")
}
