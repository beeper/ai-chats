package connector

import (
	"context"
	"testing"
)

func TestSelectedBuiltinToolsForTurn_SimpleModeEnablesOnlyWebSearch(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Search: &SearchConfig{
						Exa: ProviderExaConfig{APIKey: "test-key"},
					},
				},
			},
		},
	}

	meta := simpleModeTestMeta("openai/gpt-5.2")

	got := client.selectedBuiltinToolsForTurn(context.Background(), meta)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 tool for simple mode, got %d", len(got))
	}
	if normalizeToolAlias(got[0].Name) != ToolNameWebSearch {
		t.Fatalf("expected simple mode tool %q, got %q", ToolNameWebSearch, got[0].Name)
	}
}

func TestSelectedBuiltinToolsForTurn_NonAgentNonSimpleGetsNoTools(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Search: &SearchConfig{
						Exa: ProviderExaConfig{APIKey: "test-key"},
					},
				},
			},
		},
	}

	meta := &PortalMetadata{}

	got := client.selectedBuiltinToolsForTurn(context.Background(), meta)
	if len(got) != 0 {
		t.Fatalf("expected no builtin tools when room has no agent and is not simple mode, got %d", len(got))
	}
}
