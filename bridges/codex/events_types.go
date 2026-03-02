package codex

import (
	"strings"

	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
)

const (
	AIAuthFailed status.BridgeStateErrorCode = "ai-auth-failed"
)

type aiToastType string

const (
	aiToastTypeError aiToastType = "error"
)

func toolDisplayTitle(toolName string) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return "tool"
	}
	return toolName
}

func messageStatusForError(_ error) event.MessageStatus {
	return event.MessageStatusRetriable
}

func messageStatusReasonForError(_ error) event.MessageStatusReason {
	return event.MessageStatusGenericError
}
