package ai

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

func testToolSelectionClient(supportsToolCalling bool) *AIClient {
	return &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Search: &SearchConfig{
						Exa: ProviderExaConfig{APIKey: "test"},
					},
				},
			},
		},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			ModelCache: &ModelCache{
				Models: []ModelInfo{{
					ID:                  "openai/gpt-5.2",
					SupportsToolCalling: supportsToolCalling,
				}},
				LastRefresh:   time.Now().Unix(),
				CacheDuration: 3600,
			},
		}}},
	}
}

func TestSelectedStreamingToolDescriptorsSkipsAllToolsWhenModelCannotCallTools(t *testing.T) {
	meta := agentModeTestMeta("beeper")
	meta.RuntimeModelOverride = "openai/gpt-5.2"

	withTools := testToolSelectionClient(true).selectedStreamingToolDescriptors(context.Background(), meta, false)
	if len(withTools) == 0 {
		t.Fatal("expected tool descriptors when tool calling is supported")
	}

	withoutTools := testToolSelectionClient(false).selectedStreamingToolDescriptors(context.Background(), meta, false)
	if len(withoutTools) != 0 {
		t.Fatalf("expected no tool descriptors when tool calling is unsupported, got %#v", withoutTools)
	}
}
