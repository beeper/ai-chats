package connector

import "testing"

func TestPortalMetaNilPortal(t *testing.T) {
	meta := portalMeta(nil)
	if meta == nil {
		t.Fatal("expected non-nil portal metadata for nil portal")
	}
	if meta.ResolvedTarget != nil {
		t.Fatalf("expected nil resolved target for nil portal, got %#v", meta.ResolvedTarget)
	}
}
