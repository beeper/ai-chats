package ai

import (
	"testing"

	"github.com/beeper/agentremote/pkg/agents"
)

func TestAgentModuleValueResolvesMemoryModule(t *testing.T) {
	agent := &agents.AgentDefinition{
		MemorySearch: map[string]any{
			"enabled": true,
			"query": map[string]any{
				"max_results": 7,
			},
		},
	}

	value, ok := agentModuleValue(agent, "memory")
	if !ok {
		t.Fatal("expected memory module value")
	}
	cfg, ok := value.(*agents.MemorySearchConfig)
	if !ok {
		t.Fatalf("expected typed memory config, got %#v", value)
	}
	if cfg.Enabled == nil || !*cfg.Enabled {
		t.Fatalf("expected enabled config, got %#v", cfg)
	}
	if cfg.Query == nil || cfg.Query.MaxResults != 7 {
		t.Fatalf("expected query.max_results=7, got %#v", cfg.Query)
	}
}

func TestModuleConfigMapProjectsTypedMemoryConfig(t *testing.T) {
	enabled := true
	cfg := &agents.MemorySearchConfig{
		Enabled: &enabled,
		Query: &agents.MemorySearchQueryConfig{
			MaxResults: 5,
		},
	}

	out := moduleConfigMap(cfg)
	if out == nil {
		t.Fatal("expected module config map")
	}
	if out["enabled"] != true {
		t.Fatalf("expected enabled=true, got %#v", out["enabled"])
	}
	query, _ := out["query"].(map[string]any)
	if query["max_results"] != float64(5) {
		t.Fatalf("expected query.max_results=5, got %#v", query)
	}
}
