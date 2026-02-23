package codex

import (
	"context"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
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
	return bridgeadapter.UnsupportedMessageStatus(err)
}

func messageSendStatusError(err error, message string, reason event.MessageStatusReason) error {
	return bridgeadapter.MessageSendStatusError(err, message, reason, messageStatusForError, messageStatusReasonForError)
}

var newBrokenLoginClient = bridgeadapter.NewBrokenLoginClient
