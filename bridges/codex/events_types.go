package codex

import (
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
)

const (
	AIAuthFailed status.BridgeStateErrorCode = "ai-auth-failed"
)

func messageStatusForError(_ error) event.MessageStatus {
	return event.MessageStatusRetriable
}

func messageStatusReasonForError(_ error) event.MessageStatusReason {
	return event.MessageStatusGenericError
}
