package connector

import "maunium.net/go/mautrix/event"

func messageStatusForError(err error) event.MessageStatus {
	switch {
	case IsAuthError(err),
		IsBillingError(err),
		IsModelNotFound(err),
		ParseContextLengthError(err) != nil,
		IsImageError(err):
		return event.MessageStatusFail
	case IsRateLimitError(err), IsOverloadedError(err), IsTimeoutError(err), IsServerError(err):
		return event.MessageStatusRetriable
	default:
		// Default to retriable for transient API/generation errors.
		return event.MessageStatusRetriable
	}
}

func messageStatusReasonForError(err error) event.MessageStatusReason {
	switch {
	case IsAuthError(err), IsBillingError(err):
		return event.MessageStatusNoPermission
	case IsModelNotFound(err):
		return event.MessageStatusUnsupported
	case ParseContextLengthError(err) != nil, IsImageError(err):
		return event.MessageStatusUnsupported
	case IsRateLimitError(err), IsOverloadedError(err), IsTimeoutError(err), IsServerError(err):
		return event.MessageStatusNetworkError
	default:
		return event.MessageStatusGenericError
	}
}
