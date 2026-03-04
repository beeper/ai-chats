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

func TestZenE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for zen.test.ts pending runtime implementation")
}

func TestGithubCopilotAnthropicE2EParityScaffold(t *testing.T) {
	requirePIAIE2E(t)
	t.Skip("parity scaffold for github-copilot-anthropic.test.ts pending runtime implementation")
}
