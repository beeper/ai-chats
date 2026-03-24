package ai

import (
	"strings"

	"github.com/beeper/agentremote/pkg/agents"
)

func hasExplicitHeartbeatAgents(cfg *Config) bool {
	if cfg == nil || !cfg.agentsEnabled() || cfg.Agents == nil {
		return false
	}
	for _, entry := range cfg.Agents.List {
		if entry.Heartbeat != nil {
			return true
		}
	}
	return false
}

func resolveHeartbeatConfig(cfg *Config, agentID string) *HeartbeatConfig {
	if cfg == nil || cfg.Agents == nil {
		return nil
	}
	defaults := cfg.Agents.Defaults
	var base *HeartbeatConfig
	if defaults != nil {
		base = defaults.Heartbeat
	}
	normalized := normalizeAgentID(agentID)
	var override *HeartbeatConfig
	for _, entry := range cfg.Agents.List {
		if normalizeAgentID(entry.ID) == normalized {
			override = entry.Heartbeat
			break
		}
	}
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}
	merged := *base
	// Override only the fields explicitly provided on the agent-specific config.
	if override.Every != nil {
		merged.Every = override.Every
	}
	if override.ActiveHours != nil {
		merged.ActiveHours = override.ActiveHours
	}
	if override.Model != nil {
		merged.Model = override.Model
	}
	if override.Session != nil {
		merged.Session = override.Session
	}
	if override.Target != nil {
		merged.Target = override.Target
	}
	if override.To != nil {
		merged.To = override.To
	}
	if override.Prompt != nil {
		merged.Prompt = override.Prompt
	}
	if override.AckMaxChars != nil {
		merged.AckMaxChars = override.AckMaxChars
	}
	if override.IncludeReasoning != nil {
		merged.IncludeReasoning = override.IncludeReasoning
	}
	return &merged
}

func isHeartbeatEnabledForAgent(cfg *Config, agentID string) bool {
	resolved := normalizeAgentID(agentID)
	defaultAgent := normalizeAgentID(agents.DefaultAgentID)
	if cfg == nil {
		return resolved == defaultAgent
	}
	if !cfg.agentsEnabled() {
		return false
	}
	if cfg.Agents == nil {
		return resolved == defaultAgent
	}
	if hasExplicitHeartbeatAgents(cfg) {
		for _, entry := range cfg.Agents.List {
			if entry.Heartbeat == nil {
				continue
			}
			if normalizeAgentID(entry.ID) == resolved {
				return true
			}
		}
		return false
	}
	return resolved == defaultAgent
}

func resolveHeartbeatIntervalMs(cfg *Config, overrideEvery string, heartbeat *HeartbeatConfig) int64 {
	raw := ""
	hasRaw := false
	if strings.TrimSpace(overrideEvery) != "" {
		raw = strings.TrimSpace(overrideEvery)
		hasRaw = true
	} else if heartbeat != nil && heartbeat.Every != nil {
		raw = strings.TrimSpace(*heartbeat.Every)
		hasRaw = true
	} else if cfg != nil && cfg.Agents != nil && cfg.Agents.Defaults != nil && cfg.Agents.Defaults.Heartbeat != nil && cfg.Agents.Defaults.Heartbeat.Every != nil {
		raw = strings.TrimSpace(*cfg.Agents.Defaults.Heartbeat.Every)
		hasRaw = true
	}
	if !hasRaw {
		raw = agents.DefaultHeartbeatEvery
	}
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	ms, err := parseDurationMs(raw, "m")
	if err != nil || ms <= 0 {
		return 0
	}
	return ms
}

func resolveHeartbeatPrompt(cfg *Config, heartbeat *HeartbeatConfig, agent *agents.AgentDefinition) string {
	if agent != nil && strings.TrimSpace(agent.HeartbeatPrompt) != "" {
		return agents.ResolveHeartbeatPrompt(agent.HeartbeatPrompt)
	}
	raw := ""
	hasRaw := false
	if heartbeat != nil && heartbeat.Prompt != nil {
		raw = *heartbeat.Prompt
		hasRaw = true
	} else if cfg != nil && cfg.Agents != nil && cfg.Agents.Defaults != nil && cfg.Agents.Defaults.Heartbeat != nil && cfg.Agents.Defaults.Heartbeat.Prompt != nil {
		raw = *cfg.Agents.Defaults.Heartbeat.Prompt
		hasRaw = true
	}
	if !hasRaw {
		raw = ""
	}
	return agents.ResolveHeartbeatPrompt(raw)
}

func resolveHeartbeatAckMaxChars(cfg *Config, heartbeat *HeartbeatConfig) int {
	if heartbeat != nil && heartbeat.AckMaxChars != nil {
		if *heartbeat.AckMaxChars < 0 {
			return 0
		}
		return *heartbeat.AckMaxChars
	}
	if cfg != nil && cfg.Agents != nil && cfg.Agents.Defaults != nil && cfg.Agents.Defaults.Heartbeat != nil && cfg.Agents.Defaults.Heartbeat.AckMaxChars != nil {
		if *cfg.Agents.Defaults.Heartbeat.AckMaxChars < 0 {
			return 0
		}
		return *cfg.Agents.Defaults.Heartbeat.AckMaxChars
	}
	return agents.DefaultMaxAckChars
}
