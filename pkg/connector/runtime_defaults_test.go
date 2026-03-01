package connector

import (
	"testing"
	"time"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
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
	if connector.Config.Pruning == nil {
		t.Fatal("expected pruning defaults to be initialized")
	}
	if !connector.Config.Pruning.Enabled {
		t.Fatal("expected pruning defaults enabled")
	}
	if connector.Config.Pruning.Mode != "cache-ttl" {
		t.Fatalf("expected pruning mode cache-ttl, got %q", connector.Config.Pruning.Mode)
	}
	if connector.Config.Pruning.TTL != time.Hour {
		t.Fatalf("expected pruning ttl 1h, got %v", connector.Config.Pruning.TTL)
	}
}

func TestApplyRuntimeDefaultsKeepsExplicitPruningModeOff(t *testing.T) {
	connector := &OpenAIConnector{
		Config: Config{
			Pruning: &airuntime.PruningConfig{
				Mode:    "off",
				Enabled: false,
			},
		},
	}

	connector.applyRuntimeDefaults()

	if connector.Config.Pruning == nil {
		t.Fatal("expected pruning config to remain set")
	}
	if connector.Config.Pruning.Mode != "off" {
		t.Fatalf("expected pruning mode off to be preserved, got %q", connector.Config.Pruning.Mode)
	}
	if connector.Config.Pruning.Enabled {
		t.Fatal("expected pruning enabled=false to be preserved")
	}
	if connector.Config.Pruning.SoftTrimRatio <= 0 {
		t.Fatal("expected missing pruning numeric defaults to be filled")
	}
}
