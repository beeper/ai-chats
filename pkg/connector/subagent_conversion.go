package connector

import (
	"slices"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

func subagentsToTools(cfg *agents.SubagentConfig) *tools.SubagentConfig {
	return convertSubagentConfig(cfg, func(model, thinking string, allowAgents []string) *tools.SubagentConfig {
		return &tools.SubagentConfig{
			Model:       model,
			Thinking:    thinking,
			AllowAgents: allowAgents,
		}
	})
}

func subagentsFromTools(cfg *tools.SubagentConfig) *agents.SubagentConfig {
	return convertSubagentConfig(cfg, func(model, thinking string, allowAgents []string) *agents.SubagentConfig {
		return &agents.SubagentConfig{
			Model:       model,
			Thinking:    thinking,
			AllowAgents: allowAgents,
		}
	})
}

type subagentConfigLike interface {
	*agents.SubagentConfig | *tools.SubagentConfig
}

func convertSubagentConfig[T subagentConfigLike, R any](cfg T, build func(string, string, []string) *R) *R {
	if cfg == nil {
		return nil
	}
	allowAgents := []string(nil)
	switch typed := any(cfg).(type) {
	case *agents.SubagentConfig:
		if len(typed.AllowAgents) > 0 {
			allowAgents = slices.Clone(typed.AllowAgents)
		}
		return build(typed.Model, typed.Thinking, allowAgents)
	case *tools.SubagentConfig:
		if len(typed.AllowAgents) > 0 {
			allowAgents = slices.Clone(typed.AllowAgents)
		}
		return build(typed.Model, typed.Thinking, allowAgents)
	default:
		return nil
	}
}
