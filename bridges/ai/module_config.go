package ai

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/beeper/agentremote/pkg/agents"
)

func (oc *AIClient) agentModuleConfig(agentID string, module string) map[string]any {
	if oc == nil {
		return nil
	}
	store := &AgentStoreAdapter{client: oc}
	agent, err := store.GetAgentByID(oc.backgroundContext(context.TODO()), agentID)
	if err != nil || agent == nil {
		return nil
	}
	value, ok := agentModuleValue(agent, module)
	if !ok {
		return nil
	}
	return moduleConfigMap(value)
}

func agentModuleValue(agent *agents.AgentDefinition, module string) (any, bool) {
	if agent == nil {
		return nil, false
	}
	switch strings.ToLower(strings.TrimSpace(module)) {
	case "memory":
		cfg := normalizeMemorySearchConfig(agent.MemorySearch)
		if cfg == nil {
			return nil, false
		}
		return cfg, true
	default:
		return nil, false
	}
}

func normalizeMemorySearchConfig(raw any) any {
	switch typed := raw.(type) {
	case nil:
		return nil
	case *agents.MemorySearchConfig:
		return typed
	case agents.MemorySearchConfig:
		cfg := typed
		return &cfg
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		var cfg agents.MemorySearchConfig
		if err = json.Unmarshal(data, &cfg); err != nil {
			return nil
		}
		return &cfg
	}
}

func moduleConfigMap(raw any) map[string]any {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err = json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}
