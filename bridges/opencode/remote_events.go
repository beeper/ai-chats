package opencode

import (
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

// OpenCodeRemoteMessage is a type alias for the shared RemoteMessage.
type OpenCodeRemoteMessage = bridgeadapter.RemoteMessage

// OpenCodeRemoteEdit is a type alias for the shared RemoteEdit.
type OpenCodeRemoteEdit = bridgeadapter.RemoteEdit

func newOpenCodeMessageID() networkid.MessageID {
	return bridgeadapter.NewMessageID("opencode")
}
