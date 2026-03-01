package connector

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

// loggerFromContext returns the logger from the context if available,
// otherwise falls back to the provided logger.
func loggerFromContext(ctx context.Context, fallback *zerolog.Logger) *zerolog.Logger {
	return bridgeadapter.LoggerFromContext(ctx, fallback)
}
