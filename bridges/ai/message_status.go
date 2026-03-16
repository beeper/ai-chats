package ai

import (
	"maunium.net/go/mautrix/event"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func messageStatusForError(err error) event.MessageStatus {
	switch {
	case IsAuthError(err),
		IsPermissionDeniedError(err),
		IsBillingError(err),
		IsModelNotFound(err),
		ParseContextLengthError(err) != nil,
		IsImageError(err):
		return event.MessageStatusFail
	default:
		return event.MessageStatusRetriable
	}
}

func messageStatusReasonForError(err error) event.MessageStatusReason {
	switch airuntime.DecideFallback(err).Class {
	case airuntime.FailureClassAuth:
		return event.MessageStatusNoPermission
	case airuntime.FailureClassRateLimit, airuntime.FailureClassTimeout, airuntime.FailureClassNetwork:
		return event.MessageStatusNetworkError
	case airuntime.FailureClassContextOverflow:
		return event.MessageStatusUnsupported
	}
	switch {
	case IsAuthError(err), IsPermissionDeniedError(err), IsBillingError(err):
		return event.MessageStatusNoPermission
	case IsModelNotFound(err), ParseContextLengthError(err) != nil, IsImageError(err):
		return event.MessageStatusUnsupported
	case IsRateLimitError(err), IsOverloadedError(err), IsTimeoutError(err), IsServerError(err):
		return event.MessageStatusNetworkError
	default:
		return event.MessageStatusGenericError
	}
}
