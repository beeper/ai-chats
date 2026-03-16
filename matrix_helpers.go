package agentremote

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// LoggerFromContext returns the logger from the context if available,
// otherwise falls back to the provided logger.
func LoggerFromContext(ctx context.Context, fallback *zerolog.Logger) *zerolog.Logger {
	if ctx != nil {
		if ctxLog := zerolog.Ctx(ctx); ctxLog != nil && ctxLog.GetLevel() != zerolog.Disabled {
			return ctxLog
		}
	}
	return fallback
}

// loggerForLogin returns a logger from the context or the login's bridge logger,
// falling back to a no-op logger if neither is available.
func loggerForLogin(ctx context.Context, login *bridgev2.UserLogin) *zerolog.Logger {
	var fallback *zerolog.Logger
	if login != nil && login.Bridge != nil {
		fallback = &login.Bridge.Log
	}
	if logger := LoggerFromContext(ctx, fallback); logger != nil {
		return logger
	}
	nop := zerolog.Nop()
	return &nop
}

// IsMatrixBotUser returns true if the given user ID belongs to the bridge bot or a ghost.
func IsMatrixBotUser(ctx context.Context, bridge *bridgev2.Bridge, userID id.UserID) bool {
	if userID == "" || bridge == nil {
		return false
	}
	if bridge.Bot != nil && bridge.Bot.GetMXID() == userID {
		return true
	}
	ghost, err := bridge.GetGhostByMXID(ctx, userID)
	return err == nil && ghost != nil
}

// MatrixEventTimestamp returns the event's timestamp as a time.Time,
// falling back to time.Now() if the event is nil or has no timestamp.
func MatrixEventTimestamp(evt *event.Event) time.Time {
	if evt != nil && evt.Timestamp > 0 {
		return time.UnixMilli(evt.Timestamp)
	}
	return time.Now()
}
