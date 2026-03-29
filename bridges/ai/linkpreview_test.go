package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/shared/citations"
)

func TestPreviewCacheReturnsClones(t *testing.T) {
	cache := &previewCache{
		entries: make(map[string]*previewCacheEntry),
	}

	orig := &PreviewWithImage{
		Preview: &event.BeeperLinkPreview{
			LinkPreview: event.LinkPreview{
				Title: "original",
			},
		},
		ImageData: []byte{1, 2},
		ImageURL:  "https://example.com/image.png",
	}

	cache.set("https://example.com", orig, time.Hour)

	// Mutate original after caching; cache should be isolated.
	orig.Preview.Title = "mutated"
	orig.ImageData[0] = 9

	first := cache.get("https://example.com")
	if first == nil || first.Preview == nil {
		t.Fatal("expected cached preview")
	}
	if first.Preview.Title != "original" {
		t.Fatalf("expected original title, got %q", first.Preview.Title)
	}
	if len(first.ImageData) != 2 || first.ImageData[0] != 1 {
		t.Fatalf("expected original image data, got %v", first.ImageData)
	}

	// Mutate returned copy; subsequent fetch should be unaffected.
	first.Preview.Title = "changed"
	first.ImageData[0] = 7

	second := cache.get("https://example.com")
	if second == nil || second.Preview == nil {
		t.Fatal("expected cached preview")
	}
	if second.Preview.Title != "original" {
		t.Fatalf("expected original title on second fetch, got %q", second.Preview.Title)
	}
	if len(second.ImageData) != 2 || second.ImageData[0] != 1 {
		t.Fatalf("expected original image data on second fetch, got %v", second.ImageData)
	}
}

// tiny 1x1 red PNG for testing image downloads.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, // 8-bit RGB
	0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41, 0x54, // IDAT chunk
	0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00, 0x00, // compressed data
	0x00, 0x02, 0x00, 0x01, 0xe2, 0x21, 0xbc, 0x33, // ...
	0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, // IEND chunk
	0xae, 0x42, 0x60, 0x82,
}

func newTestImageServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(tinyPNG)
	}))
}

func TestPreviewFromCitation(t *testing.T) {
	imgServer := newTestImageServer()
	defer imgServer.Close()

	// Clear the global cache for a clean test.
	globalPreviewCache = &previewCache{entries: make(map[string]*previewCacheEntry)}

	lp := NewLinkPreviewer(DefaultLinkPreviewConfig())
	ctx := context.Background()

	citation := citations.SourceCitation{
		URL:         "https://example.com/article",
		Title:       "Test Article",
		Description: "A great article about testing.",
		SiteName:    "Example",
		Image:       imgServer.URL + "/image.png",
	}

	result := lp.PreviewFromCitation(ctx, "https://example.com/article", citation)
	if result == nil {
		t.Fatal("expected non-nil preview")
	}
	if result.Preview == nil {
		t.Fatal("expected non-nil preview.Preview")
	}
	if result.Preview.Title != "Test Article" {
		t.Fatalf("expected title 'Test Article', got %q", result.Preview.Title)
	}
	if result.Preview.Description != "A great article about testing." {
		t.Fatalf("unexpected description: %q", result.Preview.Description)
	}
	if result.Preview.SiteName != "Example" {
		t.Fatalf("unexpected site name: %q", result.Preview.SiteName)
	}
	if result.Preview.MatchedURL != "https://example.com/article" {
		t.Fatalf("unexpected matched URL: %q", result.Preview.MatchedURL)
	}
	if len(result.ImageData) == 0 {
		t.Fatal("expected image data to be downloaded")
	}
	if result.ImageURL != imgServer.URL+"/image.png" {
		t.Fatalf("unexpected image URL: %q", result.ImageURL)
	}
	if result.Preview.ImageWidth != 1 || result.Preview.ImageHeight != 1 {
		t.Fatalf("expected 1x1 image dimensions, got %dx%d", result.Preview.ImageWidth, result.Preview.ImageHeight)
	}
}

func TestPreviewFromCitation_NoImage(t *testing.T) {
	globalPreviewCache = &previewCache{entries: make(map[string]*previewCacheEntry)}

	lp := NewLinkPreviewer(DefaultLinkPreviewConfig())
	ctx := context.Background()

	citation := citations.SourceCitation{
		URL:   "https://example.com/no-image",
		Title: "No Image Article",
	}

	result := lp.PreviewFromCitation(ctx, "https://example.com/no-image", citation)
	if result == nil {
		t.Fatal("expected non-nil preview even without image")
	}
	if result.Preview.Title != "No Image Article" {
		t.Fatalf("expected title, got %q", result.Preview.Title)
	}
	if len(result.ImageData) != 0 {
		t.Fatal("expected no image data")
	}
}

func TestFetchPreviewsWithCitations_PrefersCitation(t *testing.T) {
	imgServer := newTestImageServer()
	defer imgServer.Close()

	// HTML server returns a page, but we should NOT hit it for the citation URL.
	htmlHitCount := 0
	htmlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		htmlHitCount++
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Fallback</title></head><body></body></html>`)
	}))
	defer htmlServer.Close()

	globalPreviewCache = &previewCache{entries: make(map[string]*previewCacheEntry)}

	lp := NewLinkPreviewer(DefaultLinkPreviewConfig())
	ctx := context.Background()

	citationURL := "https://example.com/exa-result"
	cits := []citations.SourceCitation{
		{
			URL:         citationURL,
			Title:       "Exa Title",
			Description: "Exa Description",
			Image:       imgServer.URL + "/thumb.png",
		},
	}

	// Request two URLs: one with citation, one without (will use HTML fallback).
	previews := lp.FetchPreviewsWithCitations(ctx, []string{citationURL, htmlServer.URL + "/other"}, cits)

	// The citation URL should produce a preview with Exa metadata.
	var citationPreview *PreviewWithImage
	for _, p := range previews {
		if p.Preview.MatchedURL == citationURL {
			citationPreview = p
		}
	}
	if citationPreview == nil {
		t.Fatal("expected preview for citation URL")
	}
	if citationPreview.Preview.Title != "Exa Title" {
		t.Fatalf("expected Exa title, got %q", citationPreview.Preview.Title)
	}
	if len(citationPreview.ImageData) == 0 {
		t.Fatal("expected image data from citation")
	}

	// The other URL should have triggered an HTML fetch.
	if htmlHitCount == 0 {
		t.Fatal("expected HTML server to be hit for non-citation URL")
	}
}

func TestPreviewsToMapSlice_IncludesImageDimensions(t *testing.T) {
	previews := []*event.BeeperLinkPreview{
		{
			LinkPreview: event.LinkPreview{
				CanonicalURL: "https://example.com",
				Title:        "Example",
				ImageURL:     id.ContentURIString("mxc://example.com/abc"),
				ImageType:    "image/png",
				ImageWidth:   800,
				ImageHeight:  600,
				ImageSize:    12345,
			},
			MatchedURL: "https://example.com",
		},
	}

	result := PreviewsToMapSlice(previews)
	if len(result) != 1 {
		t.Fatalf("expected 1 preview, got %d", len(result))
	}

	m := result[0]
	if m["og:image:width"] != 800 {
		t.Fatalf("expected og:image:width=800, got %v", m["og:image:width"])
	}
	if m["og:image:height"] != 600 {
		t.Fatalf("expected og:image:height=600, got %v", m["og:image:height"])
	}
	if m["og:image:type"] != "image/png" {
		t.Fatalf("expected og:image:type=image/png, got %v", m["og:image:type"])
	}
	if m["matrix:image:size"] != 12345 {
		t.Fatalf("expected matrix:image:size=12345, got %v", m["matrix:image:size"])
	}
}

func TestParseExistingLinkPreviewsPrefersMNewContent(t *testing.T) {
	rawContent := map[string]any{
		"com.beeper.linkpreviews": []any{
			map[string]any{
				"matched_url": "https://top-level.example",
				"og:url":      "https://top-level.example",
				"og:title":    "Top Level",
			},
		},
		"m.new_content": map[string]any{
			"com.beeper.linkpreviews": []any{
				map[string]any{
					"matched_url": "https://new-content.example",
					"og:url":      "https://new-content.example",
					"og:title":    "New Content",
				},
			},
		},
	}

	previews := ParseExistingLinkPreviews(rawContent)
	if len(previews) != 1 {
		t.Fatalf("expected one preview, got %#v", previews)
	}
	if previews[0].MatchedURL != "https://new-content.example" {
		t.Fatalf("expected m.new_content preview to win, got %#v", previews[0])
	}
}

func TestParseExistingLinkPreviewsFallsBackToTopLevel(t *testing.T) {
	rawContent := map[string]any{
		"com.beeper.linkpreviews": []any{
			map[string]any{
				"matched_url": "https://top-level.example",
				"og:url":      "https://top-level.example",
				"og:title":    "Top Level",
			},
		},
	}

	previews := ParseExistingLinkPreviews(rawContent)
	if len(previews) != 1 {
		t.Fatalf("expected one preview, got %#v", previews)
	}
	if previews[0].MatchedURL != "https://top-level.example" {
		t.Fatalf("expected top-level preview, got %#v", previews[0])
	}
}

func TestPreviewsToMapSlice_OmitsZeroDimensions(t *testing.T) {
	previews := []*event.BeeperLinkPreview{
		{
			LinkPreview: event.LinkPreview{
				CanonicalURL: "https://example.com",
				Title:        "No Image",
			},
			MatchedURL: "https://example.com",
		},
	}

	result := PreviewsToMapSlice(previews)
	if len(result) != 1 {
		t.Fatalf("expected 1 preview, got %d", len(result))
	}

	m := result[0]
	if _, ok := m["og:image:width"]; ok {
		t.Fatal("expected og:image:width to be omitted for zero value")
	}
	if _, ok := m["og:image:height"]; ok {
		t.Fatal("expected og:image:height to be omitted for zero value")
	}
	if _, ok := m["og:image:type"]; ok {
		t.Fatal("expected og:image:type to be omitted for empty string")
	}
	if _, ok := m["matrix:image:size"]; ok {
		t.Fatal("expected matrix:image:size to be omitted for zero value")
	}
}
