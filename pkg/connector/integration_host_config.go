package connector

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/beeper/ai-bridge/pkg/agents"
	integrationmemory "github.com/beeper/ai-bridge/pkg/integrations/memory"
)

type memoryAgentSearchConfig = agents.MemorySearchConfig

func resolveMemorySearchConfig(client *AIClient, agentID string) (*integrationmemory.ResolvedConfig, error) {
	if client == nil || client.connector == nil {
		return nil, errors.New("missing connector")
	}
	defaults := client.connector.Config.MemorySearch
	var overrides *agents.MemorySearchConfig

	if agentID != "" {
		store := NewAgentStoreAdapter(client)
		agent, err := store.GetAgentByID(client.backgroundContext(context.TODO()), agentID)
		if err == nil && agent != nil {
			overrides = agent.MemorySearch
		}
	}

	resolved := mergeMemorySearchConfig(defaults, overrides)
	if resolved == nil {
		return nil, errors.New("memory search disabled")
	}
	return resolved, nil
}

func mergeMemorySearchConfig(
	defaults *MemorySearchConfig,
	overrides *agents.MemorySearchConfig,
) *integrationmemory.ResolvedConfig {
	return integrationmemory.MergeSearchConfig(convertMemorySearchDefaults(defaults), overrides)
}

func convertMemorySearchDefaults(defaults *MemorySearchConfig) *agents.MemorySearchConfig {
	if defaults == nil {
		return nil
	}
	raw, err := json.Marshal(defaults)
	if err != nil {
		return nil
	}
	var out agents.MemorySearchConfig
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return &out
}
