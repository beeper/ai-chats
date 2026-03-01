package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExaProviderSearchUsesHighlightMaxCharacters(t *testing.T) {
	t.Helper()

	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("missing api key header")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("missing bearer header, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"t","url":"https://example.com","highlights":["h"]}]}`))
	}))
	defer server.Close()

	provider := &exaProvider{cfg: ExaConfig{
		BaseURL:           server.URL,
		APIKey:            "test-key",
		Type:              "auto",
		NumResults:        3,
		Highlights:        true,
		TextMaxCharacters: 777,
	}}

	_, err := provider.Search(context.Background(), Request{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	contents, ok := gotBody["contents"].(map[string]any)
	if !ok {
		t.Fatalf("expected contents object in payload")
	}
	highlights, ok := contents["highlights"].(map[string]any)
	if !ok {
		t.Fatalf("expected highlights object in payload")
	}
	if _, ok := highlights["numSentences"]; ok {
		t.Fatalf("deprecated numSentences must not be sent")
	}
	if _, ok := highlights["highlightsPerUrl"]; ok {
		t.Fatalf("deprecated highlightsPerUrl must not be sent")
	}
	if int(highlights["maxCharacters"].(float64)) != 777 {
		t.Fatalf("expected maxCharacters=777, got %#v", highlights["maxCharacters"])
	}
}
