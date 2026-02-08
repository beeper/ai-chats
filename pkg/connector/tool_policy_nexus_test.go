package connector

import (
	"context"
	"strings"
	"testing"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestNexusMCPToolsAvailableForAllAgents(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Nexus: &NexusToolsConfig{
						Enabled: boolPtr(true),
						BaseURL: "https://nexum.clay.earth",
						Token:   "test-token",
					},
				},
			},
		},
	}

	meta := &PortalMetadata{
		AgentID: "beeper",
		Capabilities: ModelCapabilities{
			SupportsToolCalling: true,
		},
	}

	// MCP tools are now available to all agents (no scoping).
	available, _, reason := oc.isToolAvailable(meta, "searchContacts")
	if !available {
		t.Fatalf("expected searchContacts to be available for beeper agent (MCP scoping removed), got reason: %s", reason)
	}
}

func TestNexusCompactContactsToolRestrictedToNexusAgent(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Nexus: &NexusToolsConfig{
						Enabled: boolPtr(true),
						BaseURL: "https://nexum.clay.earth",
						Token:   "test-token",
					},
				},
			},
		},
	}

	meta := &PortalMetadata{
		AgentID: "beeper",
		Capabilities: ModelCapabilities{
			SupportsToolCalling: true,
		},
	}

	available, source, reason := oc.isToolAvailable(meta, "contacts")
	if available {
		t.Fatalf("expected contacts to be unavailable for beeper agent")
	}
	if source != SourceAgentPolicy {
		t.Fatalf("expected SourceAgentPolicy, got %q", source)
	}
	if reason == "" {
		t.Fatalf("expected non-empty reason for restricted tool")
	}
}

func TestNexusToolsAvailableForNexusAgentWhenConfigured(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Nexus: &NexusToolsConfig{
						Enabled: boolPtr(true),
						BaseURL: "https://nexum.clay.earth",
						Token:   "test-token",
					},
				},
			},
		},
	}

	meta := &PortalMetadata{
		AgentID: "nexus",
		Capabilities: ModelCapabilities{
			SupportsToolCalling: true,
		},
	}

	available, _, reason := oc.isToolAvailable(meta, "searchContacts")
	if !available {
		t.Fatalf("expected searchContacts to be available for nexus agent, got reason: %s", reason)
	}
}

func TestNexusCompactContactsToolAvailableForNexusAgentWhenConfigured(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Nexus: &NexusToolsConfig{
						Enabled: boolPtr(true),
						BaseURL: "https://nexum.clay.earth",
						Token:   "test-token",
					},
				},
			},
		},
	}

	meta := &PortalMetadata{
		AgentID: "nexus",
		Capabilities: ModelCapabilities{
			SupportsToolCalling: true,
		},
	}

	available, _, reason := oc.isToolAvailable(meta, "contacts")
	if !available {
		t.Fatalf("expected contacts to be available for nexus agent, got reason: %s", reason)
	}
}

func TestExecuteBuiltinToolRejectsNexusToolsOutsideNexusAgent(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Nexus: &NexusToolsConfig{
						Enabled: boolPtr(true),
						BaseURL: "https://nexum.clay.earth",
						Token:   "test-token",
					},
				},
			},
		},
	}

	_, err := oc.executeBuiltinTool(context.Background(), nil, "searchContacts", "{}")
	if err == nil {
		t.Fatalf("expected executeBuiltinTool to reject Nexus tool outside Nexus agent")
	}
	if !strings.Contains(err.Error(), "restricted to the Nexus agent") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteBuiltinToolRejectsNexusCompactContactsOutsideNexusAgent(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Nexus: &NexusToolsConfig{
						Enabled: boolPtr(true),
						BaseURL: "https://nexum.clay.earth",
						Token:   "test-token",
					},
				},
			},
		},
	}

	_, err := oc.executeBuiltinTool(context.Background(), nil, "contacts", "{\"action\":\"search\",\"query\":\"test\"}")
	if err == nil {
		t.Fatalf("expected executeBuiltinTool to reject compact Nexus tool outside Nexus agent")
	}
	if !strings.Contains(err.Error(), "restricted to the Nexus agent") {
		t.Fatalf("unexpected error: %v", err)
	}
}
