package codex

import (
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

func humanUserID(loginID networkid.UserLoginID) networkid.UserID {
	return bridgeadapter.HumanUserID("codex-user", loginID)
}

// Minimal room capabilities for codex bridge rooms.
var aiBaseCaps = &event.RoomFeatures{
	ID:                  aiCapabilityID,
	MaxTextLength:       100000,
	Reply:               event.CapLevelFullySupported,
	Thread:              event.CapLevelFullySupported,
	Edit:                event.CapLevelFullySupported,
	Reaction:            event.CapLevelFullySupported,
	ReadReceipts:        true,
	TypingNotifications: true,
	DeleteChat:          true,
}
