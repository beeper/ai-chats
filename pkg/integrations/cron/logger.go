package cron

import (
	"github.com/rs/zerolog"

	croncore "github.com/beeper/ai-bridge/pkg/cron"
)

// ZeroLogger adapts zerolog to the cron core Logger interface.
type ZeroLogger struct {
	Log zerolog.Logger
}

func NewZeroLogger(log zerolog.Logger) croncore.Logger {
	return ZeroLogger{Log: log}
}

func (l ZeroLogger) Debug(msg string, fields ...any) { l.emit("debug", msg, fields...) }
func (l ZeroLogger) Info(msg string, fields ...any)  { l.emit("info", msg, fields...) }
func (l ZeroLogger) Warn(msg string, fields ...any)  { l.emit("warn", msg, fields...) }
func (l ZeroLogger) Error(msg string, fields ...any) { l.emit("error", msg, fields...) }

func (l ZeroLogger) emit(level string, msg string, fields ...any) {
	logger := l.Log
	if len(fields) == 1 {
		if m, ok := fields[0].(map[string]any); ok {
			logger = logger.With().Fields(m).Logger()
		}
	}
	switch level {
	case "debug":
		logger.Debug().Msg(msg)
	case "info":
		logger.Info().Msg(msg)
	case "warn":
		logger.Warn().Msg(msg)
	case "error":
		logger.Error().Msg(msg)
	}
}
