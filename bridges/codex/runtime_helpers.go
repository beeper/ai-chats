package codex

import (
	"context"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
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

func newBrokenLoginClient(login *bridgev2.UserLogin, connector *CodexConnector, reason string) *bridgeadapter.BrokenLoginClient {
	c := bridgeadapter.NewBrokenLoginClient(login, reason)
	c.OnLogout = func(ctx context.Context, login *bridgev2.UserLogin) {
		tmp := &CodexClient{UserLogin: login, connector: connector}
		tmp.purgeCodexHomeBestEffort(ctx)
		tmp.purgeCodexCwdsBestEffort(ctx)
		if connector != nil && login != nil {
			bridgeadapter.RemoveClientFromCache(&connector.clientsMu, connector.clients, login.ID)
		}
	}
	return c
}
