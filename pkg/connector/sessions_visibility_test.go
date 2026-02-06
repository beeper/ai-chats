package connector

import "testing"

func TestShouldExcludeModelVisiblePortal(t *testing.T) {
	if shouldExcludeModelVisiblePortal(nil) {
		t.Fatalf("nil metadata should not be excluded")
	}

	cases := []struct {
		name string
		meta PortalMetadata
	}{
		{name: "agent data", meta: PortalMetadata{IsAgentDataRoom: true}},
		{name: "global memory", meta: PortalMetadata{IsGlobalMemoryRoom: true}},
		{name: "cron", meta: PortalMetadata{IsCronRoom: true}},
		{name: "builder", meta: PortalMetadata{IsBuilderRoom: true}},
		{name: "opencode", meta: PortalMetadata{IsOpenCodeRoom: true}},
		{name: "subagent", meta: PortalMetadata{SubagentParentRoomID: "!parent:example.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !shouldExcludeModelVisiblePortal(&tc.meta) {
				t.Fatalf("expected metadata to be excluded: %+v", tc.meta)
			}
		})
	}

	visible := &PortalMetadata{
		Title: "Visible room",
	}
	if shouldExcludeModelVisiblePortal(visible) {
		t.Fatalf("expected visible room metadata to be included")
	}
}
