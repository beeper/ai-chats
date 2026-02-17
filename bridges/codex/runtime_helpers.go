package codex

import (
	"context"
	"errors"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// loggerFromContext returns the logger from the context if available,
// otherwise falls back to the provided logger.
func loggerFromContext(ctx context.Context, fallback *zerolog.Logger) *zerolog.Logger {
	if ctx != nil {
		if ctxLog := zerolog.Ctx(ctx); ctxLog != nil && ctxLog.GetLevel() != zerolog.Disabled {
			return ctxLog
		}
	}
	return fallback
}

func unsupportedMessageStatus(err error) error {
	return bridgev2.WrapErrorInStatus(err).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusUnsupported).
		WithIsCertain(true).
		WithSendNotice(true).
		WithErrorAsMessage()
}

func messageSendStatusError(err error, message string, reason event.MessageStatusReason) error {
	if err == nil {
		if message == "" {
			err = errors.New("message send failed")
		} else {
			err = errors.New(message)
		}
	}
	st := bridgev2.WrapErrorInStatus(err).WithSendNotice(true)
	st = st.WithStatus(messageStatusForError(err))
	if reason != "" {
		st = st.WithErrorReason(reason)
	} else {
		st = st.WithErrorReason(messageStatusReasonForError(err))
	}
	if message != "" {
		st = st.WithMessage(message)
	} else {
		st = st.WithErrorAsMessage()
	}
	return st
}

type brokenLoginClient = bridgeadapter.BrokenLoginClient

func newBrokenLoginClient(login *bridgev2.UserLogin, reason string) *brokenLoginClient {
	return &brokenLoginClient{
		UserLogin: login,
		Reason:    reason,
	}
}

func purgeLoginDataBestEffort(ctx context.Context, login *bridgev2.UserLogin) {
	_ = ctx
	_ = login
}
