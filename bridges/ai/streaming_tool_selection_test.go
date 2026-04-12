package ai

import (
	"context"
	"slices"
	"testing"
	"time"
)

func testBuiltinToolClient(supportsToolCalling, searchConfigured, fetchConfigured bool) *AIClient {
	searchCfg := &SearchConfig{
		Exa: ProviderExaConfig{Enabled: boolPtr(false)},
	}
	if searchConfigured {
		searchCfg.Exa = ProviderExaConfig{
			Enabled: boolPtr(true),
			APIKey:  "test-key",
		}
	}

	fetchCfg := &FetchConfig{
		Exa:    ProviderExaConfig{Enabled: boolPtr(false)},
		Direct: ProviderDirectConfig{Enabled: boolPtr(fetchConfigured)},
	}

	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Tools: ToolProvidersConfig{
					Web: &WebToolsConfig{
						Search: searchCfg,
						Fetch:  fetchCfg,
					},
				},
			},
		},
	}
	setTestLoginState(client, &loginRuntimeState{
		ModelCache: &ModelCache{
			Models: []ModelInfo{{
				ID:                  "openai/gpt-5.2",
				SupportsToolCalling: supportsToolCalling,
			}},
			LastRefresh:   time.Now().Unix(),
			CacheDuration: 3600,
		},
	})
	return client
}

func toolDefinitionNames(tools []ToolDefinition) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func TestSelectedBuiltinToolsForTurn_AgentRoomExposesBuiltinTools(t *testing.T) {
	client := testBuiltinToolClient(true, true, true)

	meta := agentModeTestMeta("beeper")
	meta.RuntimeModelOverride = "openai/gpt-5.2"

	got := client.selectedBuiltinToolsForTurn(context.Background(), meta)
	if len(got) == 0 {
		t.Fatalf("expected builtin tools for agent room")
	}
}

func TestSelectedBuiltinToolsForTurn_ModelRoomExposesModelPreset(t *testing.T) {
	client := testBuiltinToolClient(true, true, true)
	meta := modelModeTestMeta("openai/gpt-5.2")

	got := toolDefinitionNames(client.selectedBuiltinToolsForTurn(context.Background(), meta))
	want := []string{toolNameSessionStatus, toolNameWebFetch, ToolNameWebSearch}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected model room builtin tools: got %v want %v", got, want)
	}
}

func TestSelectedBuiltinToolsForTurn_ModelRoomOmitsUnavailableWebTools(t *testing.T) {
	client := testBuiltinToolClient(true, false, false)
	meta := modelModeTestMeta("openai/gpt-5.2")

	got := toolDefinitionNames(client.selectedBuiltinToolsForTurn(context.Background(), meta))
	want := []string{toolNameSessionStatus}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected model room builtin tools with web tools disabled: got %v want %v", got, want)
	}
}

func TestSelectedBuiltinToolsForTurn_ModelRoomOmitsOnlyUnavailableSearch(t *testing.T) {
	client := testBuiltinToolClient(true, false, true)
	meta := modelModeTestMeta("openai/gpt-5.2")

	got := toolDefinitionNames(client.selectedBuiltinToolsForTurn(context.Background(), meta))
	want := []string{toolNameSessionStatus, toolNameWebFetch}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected model room builtin tools with search disabled: got %v want %v", got, want)
	}
}

func TestSelectedBuiltinToolsForTurn_ModelRoomWithoutToolCallingGetsNoTools(t *testing.T) {
	client := testBuiltinToolClient(false, true, true)
	meta := modelModeTestMeta("openai/gpt-5.2")

	got := client.selectedBuiltinToolsForTurn(context.Background(), meta)
	if len(got) != 0 {
		t.Fatalf("expected no builtin tools when model does not support tool calling, got %d", len(got))
	}
}
