package cron

import (
	"strings"

	cronlib "github.com/robfig/cron/v3"
)

func normalizeString(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// cronParser is a shared cron expression parser used by schedule computation and validation.
var cronParser = cronlib.NewParser(cronlib.Minute | cronlib.Hour | cronlib.Dom | cronlib.Month | cronlib.Dow | cronlib.Descriptor)
