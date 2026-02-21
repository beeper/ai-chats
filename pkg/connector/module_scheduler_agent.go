package connector

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/agents"
	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
)

func resolveCronAgentID(raw string, cfg *Config) string {
	return integrationcron.ResolveCronAgentID(
		raw,
		agents.DefaultAgentID,
		normalizeAgentID,
		func(normalized string) bool {
			if cfg == nil || cfg.Agents == nil {
				return false
			}
			for _, entry := range cfg.Agents.List {
				if normalizeAgentID(entry.ID) == strings.TrimSpace(normalized) {
					return true
				}
			}
			return false
		},
	)
}
