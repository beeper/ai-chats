package codex

import (
	"strings"

	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

// Constants

const (
	AIAuthFailed   status.BridgeStateErrorCode = "ai-auth-failed"
	aiCapabilityID                             = "com.beeper.ai.v1"
)

// Room capabilities for codex bridge rooms.
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

func humanUserID(loginID networkid.UserLoginID) networkid.UserID {
	return bridgeadapter.HumanUserID("codex-user", loginID)
}

func normalizeToolAlias(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func messageStatusForError(_ error) event.MessageStatus {
	return event.MessageStatusRetriable
}

func messageStatusReasonForError(_ error) event.MessageStatusReason {
	return event.MessageStatusGenericError
}
