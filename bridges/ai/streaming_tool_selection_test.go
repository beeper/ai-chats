package ai

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

func TestSelectedBuiltinToolsForTurn_AgentRoomExposesBuiltinTools(t *testing.T) {
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
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			ModelCache: &ModelCache{
				Models: []ModelInfo{{
					ID:                  "openai/gpt-5.2",
					SupportsToolCalling: true,
				}},
				LastRefresh:   time.Now().Unix(),
				CacheDuration: 3600,
			},
		}}},
	}

	meta := agentModeTestMeta("beeper")
	meta.RuntimeModelOverride = "openai/gpt-5.2"

	got := client.selectedBuiltinToolsForTurn(context.Background(), meta)
	if len(got) == 0 {
		t.Fatalf("expected builtin tools for agent room")
	}
}

func TestSelectedBuiltinToolsForTurn_ModelRoomGetsNoTools(t *testing.T) {
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
		t.Fatalf("expected no builtin tools when room has no assigned agent, got %d", len(got))
	}
}
