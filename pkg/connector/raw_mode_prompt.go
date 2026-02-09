package connector

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

// buildRawModeSystemPrompt returns the system prompt for raw/playground rooms.
// Raw mode must be simple: a single system prompt with only time + web_search hints appended.
func (oc *AIClient) buildRawModeSystemPrompt(meta *PortalMetadata) string {
	base := defaultRawModeSystemPrompt
	if meta != nil {
		if v := strings.TrimSpace(meta.SystemPrompt); v != "" {
			base = v
		}
	}

	timezone, _ := oc.resolveUserTimezone()
	now := formatCronTime(timezone)

	lines := []string{
		strings.TrimSpace(base),
		"Current time: " + now,
	}
	// Only advertise tools when they are actually enabled for this portal.
	if meta != nil && oc != nil && oc.isToolEnabled(meta, toolspec.WebSearchName) {
		lines = append(lines, "Web search: available via web_search.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
