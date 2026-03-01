package codex

import (
	"errors"
	"strings"

	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
)

const (
	AIAuthFailed status.BridgeStateErrorCode = "ai-auth-failed"
)

var (
	ErrApprovalMissingID      = errors.New("missing approval id")
	ErrApprovalMissingRoom    = errors.New("missing room id")
	ErrApprovalOnlyOwner      = errors.New("only the owner can approve")
	ErrApprovalUnknown        = errors.New("unknown or expired approval id")
	ErrApprovalWrongRoom      = errors.New("approval id does not belong to this room")
	ErrApprovalExpired        = errors.New("approval expired")
	ErrApprovalAlreadyHandled = errors.New("approval already resolved")
)

type aiToastType string

const (
	aiToastTypeError aiToastType = "error"
)

func approvalErrorToastText(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrApprovalOnlyOwner):
		return "Only the owner can approve."
	case errors.Is(err, ErrApprovalWrongRoom):
		return "That approval request belongs to a different room."
	case errors.Is(err, ErrApprovalExpired), errors.Is(err, ErrApprovalUnknown):
		return "That approval request is expired or no longer valid."
	case errors.Is(err, ErrApprovalAlreadyHandled):
		return "That approval request was already handled."
	case errors.Is(err, ErrApprovalMissingID):
		return "Missing approval ID."
	default:
		return strings.TrimSpace(err.Error())
	}
}

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
