package connector

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

func newTestAIClientWithConfig(cfg Config) *AIClient {
	login := &database.UserLogin{Metadata: &UserLoginMetadata{
		Provider: ProviderOpenAI,
		ModelCache: &ModelCache{Models: []ModelInfo{
			{ID: "openai/gpt-5.2", SupportsToolCalling: true},
		}},
	}}
	userLogin := &bridgev2.UserLogin{UserLogin: login}
	return &AIClient{
		UserLogin: userLogin,
		connector: &OpenAIConnector{Config: cfg},
	}
}

func TestApplyPatchAvailability_DisabledByDefault(t *testing.T) {
	oc := newTestAIClientWithConfig(Config{})
	meta := simpleModeTestMeta("openai/gpt-5.2")

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
	meta := simpleModeTestMeta("openai/gpt-5.2")

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
	meta := simpleModeTestMeta("openai/gpt-5.2")

	available, _, _ := oc.isToolAvailable(meta, ToolNameApplyPatch)
	if available {
		t.Fatalf("expected apply_patch to be unavailable when model not allowlisted")
	}
}
