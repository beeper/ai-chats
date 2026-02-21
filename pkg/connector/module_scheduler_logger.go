package connector

import (
	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
	"github.com/rs/zerolog"
)

type cronLogger = integrationcron.ZeroLogger

func newCronLogger(log zerolog.Logger) cronLogger {
	return integrationcron.ZeroLogger{Log: log}
}
