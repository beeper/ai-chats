package connector

import (
	"strings"
	"time"
)

// buildRawModeSystemPrompt returns the system prompt for raw/playground rooms.
// Raw mode must be simple: a single system prompt with only the current time appended.
func (oc *AIClient) buildRawModeSystemPrompt(meta *PortalMetadata) string {
	base := defaultRawModeSystemPrompt
	if meta != nil {
		if v := strings.TrimSpace(meta.SystemPrompt); v != "" {
			base = v
		}
	}

	timezone, _ := oc.resolveUserTimezone()
	now := formatCurrentTimeForPrompt(timezone)

	lines := []string{
		strings.TrimSpace(base),
		"Current time: " + now,
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatCurrentTimeForPrompt(timezone string) string {
	loc := time.UTC
	if tz := strings.TrimSpace(timezone); tz != "" {
		if loaded, err := time.LoadLocation(tz); err == nil {
			loc = loaded
		}
	}
	now := time.Now().In(loc)
	return now.Format("Monday, January 2, 2006 - 3:04 PM (MST)")
}
