package connector

import (
	"github.com/rs/zerolog"
)

type cronLogger struct {
	log zerolog.Logger
}

func (l cronLogger) Debug(msg string, fields ...any) { l.emit("debug", msg, fields...) }
func (l cronLogger) Info(msg string, fields ...any)  { l.emit("info", msg, fields...) }
func (l cronLogger) Warn(msg string, fields ...any)  { l.emit("warn", msg, fields...) }
func (l cronLogger) Error(msg string, fields ...any) { l.emit("error", msg, fields...) }

func (l cronLogger) emit(level string, msg string, fields ...any) {
	logger := l.log
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
