package cron

import "strings"

func normalizeString(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
