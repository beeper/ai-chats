package streamtransport

import "testing"

func TestBuildDebouncedEditContent_WithoutEventID(t *testing.T) {
	content := BuildDebouncedEditContent(DebouncedEditParams{
		PortalMXID:  "test-room",
		Force:       true,
		VisibleBody: "hello",
	})
	if content == nil {
		t.Fatal("expected debounced edit content without event ID")
	}
	if content.Body == "" {
		t.Fatal("expected non-empty body")
	}
}
