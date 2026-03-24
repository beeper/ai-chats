package ai

import (
	"testing"

	"github.com/beeper/agentremote/pkg/agents"
)

func TestIsHeartbeatEnabledForAgent_DefaultFallback(t *testing.T) {
	defaultAgent := normalizeAgentID(agents.DefaultAgentID)
	if !isHeartbeatEnabledForAgent(nil, defaultAgent) {
		t.Fatalf("expected default agent heartbeat enabled when config is nil")
	}
	if isHeartbeatEnabledForAgent(nil, "custom") {
		t.Fatalf("expected non-default agent heartbeat disabled when config is nil")
	}

	cfg := &Config{}
	if !isHeartbeatEnabledForAgent(cfg, defaultAgent) {
		t.Fatalf("expected default agent heartbeat enabled when agents config is missing")
	}
	if isHeartbeatEnabledForAgent(cfg, "custom") {
		t.Fatalf("expected non-default agent heartbeat disabled when agents config is missing")
	}
}

func TestIsHeartbeatEnabledForAgent_ExplicitAgentsMode(t *testing.T) {
	cfg := &Config{
		Agents: &AgentsConfig{
			List: []AgentEntryConfig{
				{ID: "worker", Heartbeat: &HeartbeatConfig{}},
				{ID: "idle"},
			},
		},
	}
	if !isHeartbeatEnabledForAgent(cfg, "worker") {
		t.Fatalf("expected explicit heartbeat agent to be enabled")
	}
	if isHeartbeatEnabledForAgent(cfg, normalizeAgentID(agents.DefaultAgentID)) {
		t.Fatalf("expected default agent to be disabled in explicit heartbeat mode")
	}
	if isHeartbeatEnabledForAgent(cfg, "idle") {
		t.Fatalf("expected agent without heartbeat block to be disabled in explicit heartbeat mode")
	}
}

func TestIsHeartbeatEnabledForAgent_DisabledByConfig(t *testing.T) {
	disabled := false
	cfg := &Config{
		Agents: &AgentsConfig{
			Enabled: &disabled,
		},
	}
	if isHeartbeatEnabledForAgent(cfg, normalizeAgentID(agents.DefaultAgentID)) {
		t.Fatal("expected heartbeat to be disabled when agents are disabled")
	}
	if got := resolveHeartbeatAgents(cfg); len(got) != 0 {
		t.Fatalf("expected no heartbeat agents when disabled, got %#v", got)
	}
}
