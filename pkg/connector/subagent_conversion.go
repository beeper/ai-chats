package connector

import (
	"slices"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

func subagentsToTools(cfg *agents.SubagentConfig) *tools.SubagentConfig {
	if cfg == nil {
		return nil
	}
	out := &tools.SubagentConfig{
		Model:    cfg.Model,
		Thinking: cfg.Thinking,
	}
	if len(cfg.AllowAgents) > 0 {
		out.AllowAgents = slices.Clone(cfg.AllowAgents)
	}
	return out
}

func subagentsFromTools(cfg *tools.SubagentConfig) *agents.SubagentConfig {
	if cfg == nil {
		return nil
	}
	out := &agents.SubagentConfig{
		Model:    cfg.Model,
		Thinking: cfg.Thinking,
	}
	if len(cfg.AllowAgents) > 0 {
		out.AllowAgents = slices.Clone(cfg.AllowAgents)
	}
	return out
}
