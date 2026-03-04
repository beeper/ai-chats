package e2e

import (
	"os"
	"testing"
)

// Scaffolding parity target for pi-mono/packages/ai/test/abort.test.ts.
func TestAbortE2EParityScaffold(t *testing.T) {
	if os.Getenv("PI_AI_E2E") == "" {
		t.Skip("set PI_AI_E2E=1 to enable ai package e2e tests")
	}
	t.Skip("abort e2e parity test pending full provider runtime port")
}
