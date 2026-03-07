package codex

import "testing"

func TestExtractArtifactRecord_PrefersFirstNonEmptyStringKey(t *testing.T) {
	record := map[string]any{
		"url":         "   ",
		"downloadUrl": " https://example.com/file.pdf ",
		"filename":    " report.pdf ",
	}

	doc, file := extractArtifactRecord(record)

	if doc.Filename != "report.pdf" {
		t.Fatalf("expected trimmed filename, got %q", doc.Filename)
	}
	if file.URL != "https://example.com/file.pdf" {
		t.Fatalf("expected fallback url key to be used, got %q", file.URL)
	}
}

func TestExtractArtifactRecord_IgnoresNonStringFallbackValues(t *testing.T) {
	record := map[string]any{
		"url":        123,
		"file_url":   " https://example.com/file.txt ",
		"file_id":    " doc-1 ",
		"mime_type":  " text/plain ",
		"documentId": 456,
	}

	doc, file := extractArtifactRecord(record)

	if doc.ID != "doc-1" {
		t.Fatalf("expected string fallback id, got %q", doc.ID)
	}
	if doc.MediaType != "text/plain" {
		t.Fatalf("expected trimmed media type, got %q", doc.MediaType)
	}
	if file.URL != "https://example.com/file.txt" {
		t.Fatalf("expected string fallback url, got %q", file.URL)
	}
}
