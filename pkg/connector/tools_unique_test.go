package connector

import (
	"testing"

	agenttools "github.com/beeper/ai-bridge/pkg/agents/tools"
)

// Guard against provider errors like "tools: Tool names must be unique".
// Verifies uniqueness across builtin tools and boss tools we send to providers.
func TestToolNamesUnique(t *testing.T) {
	// Builtin tools
	builtinSeen := make(map[string]struct{})
	for _, tool := range BuiltinTools() {
		if tool.Name == "" {
			t.Fatalf("builtin tool has empty name: %+v", tool)
		}
		if _, ok := builtinSeen[tool.Name]; ok {
			t.Fatalf("duplicate builtin tool name %q", tool.Name)
		}
		builtinSeen[tool.Name] = struct{}{}
	}

	// Boss tools (combined with builtin in boss-agent rooms)
	for _, tool := range agenttools.BossTools() {
		if tool.Name == "" {
			t.Fatalf("boss tool has empty name: %+v", tool)
		}
		if _, ok := builtinSeen[tool.Name]; ok {
			t.Fatalf("duplicate tool name %q between builtin and boss", tool.Name)
		}
	}

	// Session tools (combined with builtin in non-boss rooms)
	for _, tool := range agenttools.SessionTools() {
		if tool.Name == "" {
			t.Fatalf("session tool has empty name: %+v", tool)
		}
		if _, ok := builtinSeen[tool.Name]; ok {
			t.Fatalf("duplicate tool name %q between builtin and session", tool.Name)
		}
	}
}
