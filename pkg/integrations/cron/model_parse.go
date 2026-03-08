package cron

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	cronlib "github.com/robfig/cron/v3"
)

var (
	isoTZRe       = regexp.MustCompile(`(?i)(Z|[+-]\d{2}:?\d{2})$`)
	isoDateRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	isoDateTimeRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T`)
	cronParser    = cronlib.NewParser(cronlib.Minute | cronlib.Hour | cronlib.Dom | cronlib.Month | cronlib.Dow | cronlib.Descriptor)
)

func normalizeString(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizeUTCISO(raw string) string {
	if isoTZRe.MatchString(raw) {
		return raw
	}
	if isoDateRe.MatchString(raw) {
		return raw + "T00:00:00Z"
	}
	if isoDateTimeRe.MatchString(raw) {
		return raw + "Z"
	}
	return raw
}

func parseAbsoluteTimeMs(raw string) (int64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}
	if ts, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		if ts > 0 {
			return ts, true
		}
		return 0, false
	}
	if t, err := time.Parse(time.RFC3339, normalizeUTCISO(trimmed)); err == nil {
		return t.UTC().UnixMilli(), true
	}
	return 0, false
}
