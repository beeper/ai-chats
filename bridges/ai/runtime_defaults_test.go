package ai

import (
	"testing"
	"time"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func TestApplyRuntimeDefaultsSetsPruningDefaults(t *testing.T) {
	connector := &OpenAIConnector{}

	connector.applyRuntimeDefaults()

	if connector.Config.ModelCacheDuration != 6*time.Hour {
		t.Fatalf("expected model cache duration 6h, got %v", connector.Config.ModelCacheDuration)
	}
	if connector.Config.Bridge.CommandPrefix != "!ai" {
		t.Fatalf("expected command prefix !ai, got %q", connector.Config.Bridge.CommandPrefix)
	}
	if connector.Config.Agents == nil || connector.Config.Agents.Defaults == nil || connector.Config.Agents.Defaults.Compaction == nil {
		t.Fatal("expected compaction defaults to be initialized")
	}
	if !connector.Config.Agents.Defaults.Compaction.Enabled {
		t.Fatal("expected pruning defaults enabled")
	}
	if connector.Config.Agents.Defaults.Compaction.Mode != "cache-ttl" {
		t.Fatalf("expected pruning mode cache-ttl, got %q", connector.Config.Agents.Defaults.Compaction.Mode)
	}
	if connector.Config.Agents.Defaults.Compaction.TTL != time.Hour {
		t.Fatalf("expected pruning ttl 1h, got %v", connector.Config.Agents.Defaults.Compaction.TTL)
	}
}

func TestApplyRuntimeDefaultsKeepsExplicitPruningModeOff(t *testing.T) {
	connector := &OpenAIConnector{
		Config: Config{
			Agents: &AgentsConfig{Defaults: &AgentDefaultsConfig{
				Compaction: &airuntime.PruningConfig{
					Mode:    "off",
					Enabled: false,
				},
			}},
		},
	}

	connector.applyRuntimeDefaults()

	if connector.Config.Agents == nil || connector.Config.Agents.Defaults == nil || connector.Config.Agents.Defaults.Compaction == nil {
		t.Fatal("expected pruning config to remain set")
	}
	if connector.Config.Agents.Defaults.Compaction.Mode != "off" {
		t.Fatalf("expected pruning mode off to be preserved, got %q", connector.Config.Agents.Defaults.Compaction.Mode)
	}
	if connector.Config.Agents.Defaults.Compaction.Enabled {
		t.Fatal("expected pruning enabled=false to be preserved")
	}
	if connector.Config.Agents.Defaults.Compaction.SoftTrimRatio <= 0 {
		t.Fatal("expected missing pruning numeric defaults to be filled")
	}
}
