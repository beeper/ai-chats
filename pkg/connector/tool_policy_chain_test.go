package connector

import "testing"

func TestBuildToolPolicyContext_TreatsOpenClawReservedToolsAsCore(t *testing.T) {
	oc := &AIClient{}
	ctx := oc.buildToolPolicyContext(nil)

	// These tools may not be exposed by ai-bridge, but configs may refer to them.
	// We still want them considered "core" so allowlists don't get stripped.
	for _, name := range []string{"exec", "process", "browser", "canvas", "nodes", "gateway"} {
		if _, ok := ctx.coreTools[name]; !ok {
			t.Fatalf("expected reserved tool %q to be treated as core", name)
		}
	}
}
