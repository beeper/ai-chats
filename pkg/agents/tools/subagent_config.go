package tools

import "slices"

// SubagentConfig mirrors OpenClaw-style subagent defaults for tools API payloads.
type SubagentConfig struct {
	Model       string   `json:"model,omitempty"`
	Thinking    string   `json:"thinking,omitempty"`
	AllowAgents []string `json:"allowAgents,omitempty"`
}

func cloneSubagentConfig(cfg *SubagentConfig) *SubagentConfig {
	if cfg == nil {
		return nil
	}
	out := &SubagentConfig{
		Model:    cfg.Model,
		Thinking: cfg.Thinking,
	}
	if len(cfg.AllowAgents) > 0 {
		out.AllowAgents = slices.Clone(cfg.AllowAgents)
	}
	return out
}
