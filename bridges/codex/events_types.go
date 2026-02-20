package codex

import (
	"errors"
	"strings"

	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/matrixevents"
)

const (
	RelReplace   = matrixevents.RelReplace
	RelReference = matrixevents.RelReference
	RelThread    = matrixevents.RelThread
	RelInReplyTo = matrixevents.RelInReplyTo
)

const (
	BeeperAIKey           = matrixevents.BeeperAIKey
	BeeperAIToolCallKey   = matrixevents.BeeperAIToolCallKey
	BeeperAIToolResultKey = matrixevents.BeeperAIToolResultKey
)

var ToolCallEventType = matrixevents.ToolCallEventType
var ToolResultEventType = matrixevents.ToolResultEventType

type ToolStatus string

const (
	ToolStatusPending          ToolStatus = "pending"
	ToolStatusRunning          ToolStatus = "running"
	ToolStatusCompleted        ToolStatus = "completed"
	ToolStatusFailed           ToolStatus = "failed"
	ToolStatusApprovalRequired ToolStatus = "approval_required"
)

type ToolType string

const (
	ToolTypeProvider ToolType = "provider"
)

type ResultStatus string

const (
	ResultStatusSuccess ResultStatus = "success"
	ResultStatusError   ResultStatus = "error"
	ResultStatusDenied  ResultStatus = "denied"
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

func messageStatusForError(err error) event.MessageStatus {
	if err == nil {
		return event.MessageStatusFail
	}
	return event.MessageStatusRetriable
}

func messageStatusReasonForError(err error) event.MessageStatusReason {
	if err == nil {
		return event.MessageStatusGenericError
	}
	return event.MessageStatusGenericError
}
