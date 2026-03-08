package connector

import (
	"testing"

	"github.com/beeper/ai-bridge/pkg/shared/citations"
	"github.com/beeper/ai-bridge/pkg/shared/streamui"
)

func TestBuildFinalEditUIMessage_IncludesSourceAndFileParts(t *testing.T) {
	oc := &AIClient{}
	state := &streamingState{
		turnID: "turn-1",
		sourceCitations: []citations.SourceCitation{{
			URL:      "https://example.com",
			Title:    "Example",
			SiteName: "Example Site",
		}},
		sourceDocuments: []citations.SourceDocument{{
			ID:        "doc-1",
			Title:     "Doc",
			Filename:  "doc.txt",
			MediaType: "text/plain",
		}},
		generatedFiles: []citations.GeneratedFilePart{{
			URL:       "mxc://example/file",
			MediaType: "image/png",
		}},
	}
	state.accumulated.WriteString("hello")
	streamui.ApplyChunk(&state.ui, map[string]any{"type": "start", "messageId": "turn-1"})
	streamui.ApplyChunk(&state.ui, map[string]any{"type": "text-start", "id": "text-1"})
	streamui.ApplyChunk(&state.ui, map[string]any{"type": "text-delta", "id": "text-1", "delta": "hello"})
	streamui.ApplyChunk(&state.ui, map[string]any{"type": "text-end", "id": "text-1"})

	ui := oc.buildFinalEditUIMessage(state, simpleModeTestMeta("openai/gpt-4o"), nil)
	if ui == nil {
		t.Fatalf("expected final edit UI message")
	}

	partsAny, ok := ui["parts"].([]any)
	if !ok {
		partsRaw, ok := ui["parts"].([]map[string]any)
		if !ok {
			t.Fatalf("expected parts array, got %T", ui["parts"])
		}
		partsAny = make([]any, 0, len(partsRaw))
		for _, part := range partsRaw {
			partsAny = append(partsAny, part)
		}
	}

	foundText := false
	foundSourceURL := false
	foundSourceDocument := false
	foundFile := false
	foundCamelSiteName := false
	foundSnakeSiteName := false
	for _, rawPart := range partsAny {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		switch part["type"] {
		case "text":
			foundText = true
		case "source-url":
			foundSourceURL = true
			meta, ok := part["providerMetadata"].(map[string]any)
			if ok {
				_, foundCamelSiteName = meta["siteName"]
				_, foundSnakeSiteName = meta["site_name"]
			}
		case "source-document":
			foundSourceDocument = true
		case "file":
			foundFile = true
		}
	}

	if !foundText || !foundSourceURL || !foundSourceDocument || !foundFile {
		t.Fatalf("missing expected part types (text=%v source-url=%v source-document=%v file=%v)", foundText, foundSourceURL, foundSourceDocument, foundFile)
	}
	if !foundCamelSiteName || !foundSnakeSiteName {
		t.Fatalf("expected source-url providerMetadata to include both siteName and site_name (siteName=%v site_name=%v)", foundCamelSiteName, foundSnakeSiteName)
	}
}
