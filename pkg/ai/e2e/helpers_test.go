package e2e

import (
	"os"
	"testing"
)

func requirePIAIE2E(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping e2e tests in short mode")
	}
	if os.Getenv("PI_AI_E2E") == "" {
		t.Skip("set PI_AI_E2E=1 to enable ai package e2e tests")
	}
}
