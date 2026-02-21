package connector

import (
	"strings"

	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
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
	now := integrationcron.FormatCronTime(timezone)

	lines := []string{
		strings.TrimSpace(base),
		"Current time: " + now,
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
