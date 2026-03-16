package codex

import (
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
)

const aiCapabilityID = "com.beeper.ai.v1"

func humanUserID(loginID networkid.UserLoginID) networkid.UserID {
	return agentremote.HumanUserID("codex-user", loginID)
}

// Minimal room capabilities for codex bridge rooms.
var aiBaseCaps = agentremote.BuildRoomFeatures(agentremote.RoomFeaturesParams{
	ID:                  aiCapabilityID,
	MaxTextLength:       100000,
	Reply:               event.CapLevelFullySupported,
	Thread:              event.CapLevelFullySupported,
	Edit:                event.CapLevelFullySupported,
	Reaction:            event.CapLevelFullySupported,
	ReadReceipts:        true,
	TypingNotifications: true,
	DeleteChat:          true,
})
