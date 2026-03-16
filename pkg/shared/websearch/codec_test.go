package websearch

import (
	"testing"

	"github.com/beeper/agentremote/pkg/search"
)

func TestRequestFromArgs(t *testing.T) {
	req, err := RequestFromArgs(map[string]any{
		"query":       "  test query  ",
		"count":       3,
		"country":     " nl ",
		"search_lang": " en ",
		"ui_lang":     " en ",
		"freshness":   " week ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Query != "test query" || req.Count != 3 || req.Country != "nl" || req.SearchLang != "en" || req.UILang != "en" || req.Freshness != "week" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

func TestPayloadRoundTripResults(t *testing.T) {
	payload := PayloadFromResponse(&search.Response{
		Query:    "query",
		Provider: "exa",
		Count:    1,
		Results: []search.Result{
			{
				ID:          "id-1",
				Title:       "Title",
				URL:         "https://example.com",
				Description: "Description",
				Published:   "2026-03-14",
				SiteName:    "example.com",
				Author:      "Author",
				Image:       "https://example.com/image.png",
				Favicon:     "https://example.com/favicon.ico",
			},
		},
	})

	results := ResultsFromPayload(payload)
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].ID != "id-1" || results[0].URL != "https://example.com" || results[0].Author != "Author" {
		t.Fatalf("unexpected result: %#v", results[0])
	}
}
