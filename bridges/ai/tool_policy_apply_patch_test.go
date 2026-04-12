package ai

import "testing"

func newTestAIClientWithConfig(cfg Config) *AIClient {
	client := newTestAIClientWithProvider(ProviderOpenAI)
	client.connector = &OpenAIConnector{Config: cfg}
	setTestLoginState(client, &loginRuntimeState{
		ModelCache: &ModelCache{Models: []ModelInfo{
			{ID: "openai/gpt-5.2", SupportsToolCalling: true},
		}},
	})
	return client
}

func TestApplyPatchAvailability_DisabledByDefault(t *testing.T) {
	oc := newTestAIClientWithConfig(Config{})
	meta := modelModeTestMeta("openai/gpt-5.2")

	available, _, _ := oc.isToolAvailable(meta, ToolNameApplyPatch)
	if available {
		t.Fatalf("expected apply_patch to be unavailable by default")
	}
}

func TestApplyPatchAvailability_EnabledWithoutAllowlist(t *testing.T) {
	enabled := true
	oc := newTestAIClientWithConfig(Config{
		Tools: ToolProvidersConfig{
			VFS: &VFSToolsConfig{
				ApplyPatch: &ApplyPatchToolsConfig{
					Enabled: &enabled,
				},
			},
		},
	})
	meta := modelModeTestMeta("openai/gpt-5.2")

	available, _, _ := oc.isToolAvailable(meta, ToolNameApplyPatch)
	if !available {
		t.Fatalf("expected apply_patch to be available when enabled")
	}
}

func TestApplyPatchAvailability_AllowlistMismatch(t *testing.T) {
	enabled := true
	oc := newTestAIClientWithConfig(Config{
		Tools: ToolProvidersConfig{
			VFS: &VFSToolsConfig{
				ApplyPatch: &ApplyPatchToolsConfig{
					Enabled:     &enabled,
					AllowModels: []string{"anthropic/claude-opus-4.6"},
				},
			},
		},
	})
	meta := modelModeTestMeta("openai/gpt-5.2")

	available, _, _ := oc.isToolAvailable(meta, ToolNameApplyPatch)
	if available {
		t.Fatalf("expected apply_patch to be unavailable when model not allowlisted")
	}
}
