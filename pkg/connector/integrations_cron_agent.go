package connector

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/agents"
)

func resolveCronAgentID(raw string, cfg *Config) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "main") {
		return agents.DefaultAgentID
	}
	normalized := normalizeAgentID(trimmed)
	if cfg != nil && cfg.Agents != nil {
		for _, entry := range cfg.Agents.List {
			if normalizeAgentID(entry.ID) == normalized {
				return normalized
			}
		}
	}
	return agents.DefaultAgentID
}
