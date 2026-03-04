package e2e

import (
	"os"
	"testing"
)

func requirePIAIE2E(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping e2e parity scaffolds in short mode")
	}
	if os.Getenv("PI_AI_E2E") == "" {
		t.Skip("set PI_AI_E2E=1 to enable ai package e2e tests")
	}
}

func TestInterleavedThinkingE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for interleaved-thinking.test.ts pending runtime implementation")
}

func TestBedrockModelsE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for bedrock-models.test.ts pending runtime implementation")
}

func TestToolCallIDNormalizationE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for tool-call-id-normalization.test.ts pending runtime implementation")
}

func TestAnthropicToolNameNormalizationE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for anthropic-tool-name-normalization.test.ts pending runtime implementation")
}

func TestCrossProviderHandoffE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for cross-provider-handoff.test.ts pending runtime implementation")
}

func TestGoogleGeminiCLIEmptyStreamE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for google-gemini-cli-empty-stream.test.ts pending runtime implementation")
}

func TestXhighE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for xhigh.test.ts pending runtime implementation")
}

func TestZenE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for zen.test.ts pending runtime implementation")
}

func TestEmptyE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for empty.test.ts pending runtime implementation")
}

func TestImageToolResultE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for image-tool-result.test.ts pending runtime implementation")
}

func TestGoogleGeminiCliClaudeThinkingHeaderE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for google-gemini-cli-claude-thinking-header.test.ts pending runtime implementation")
}

func TestGithubCopilotAnthropicE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for github-copilot-anthropic.test.ts pending runtime implementation")
}
