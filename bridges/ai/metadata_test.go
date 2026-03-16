package ai

import "testing"

func TestClonePortalMetadataDeepCopiesConfig(t *testing.T) {
	orig := &PortalMetadata{
		PDFConfig: &PDFConfig{Engine: "mistral"},
	}

	clone := clonePortalMetadata(orig)
	if clone == nil {
		t.Fatal("expected clone to be non-nil")
	}
	if clone == orig {
		t.Fatal("expected clone to be a different pointer")
	}
	if clone.PDFConfig == orig.PDFConfig {
		t.Fatal("expected PDF config to be copied")
	}

	clone.PDFConfig.Engine = "other"

	if orig.PDFConfig.Engine != "mistral" {
		t.Fatalf("expected original PDF engine to remain, got %q", orig.PDFConfig.Engine)
	}
}
