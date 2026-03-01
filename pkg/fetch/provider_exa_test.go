package fetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExaProviderFetchUsesConfigMaxCharsByDefault(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"results":[{"url":"https://example.com/final","text":"ok"}],"statuses":[{"id":"https://example.com","status":"success"}]}`))
	}))
	defer server.Close()

	provider := &exaProvider{cfg: ExaConfig{
		BaseURL:           server.URL,
		APIKey:            "test-key",
		IncludeText:       true,
		TextMaxCharacters: 1234,
	}}

	resp, err := provider.Fetch(context.Background(), Request{URL: "https://example.com", ExtractMode: "markdown"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text, ok := gotBody["text"].(map[string]any)
	if !ok {
		t.Fatalf("expected text object in payload, got %#v", gotBody["text"])
	}
	if int(text["maxCharacters"].(float64)) != 1234 {
		t.Fatalf("expected maxCharacters=1234, got %#v", text["maxCharacters"])
	}

	if resp.FinalURL != "https://example.com/final" {
		t.Fatalf("unexpected final url: %q", resp.FinalURL)
	}
	if resp.Text != "ok" {
		t.Fatalf("unexpected text: %q", resp.Text)
	}
}

func TestExaProviderFetchUsesRequestMaxCharsOverride(t *testing.T) {
	t.Helper()

	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"url":"https://example.com","text":"ok"}],"statuses":[{"id":"https://example.com","status":"success"}]}`))
	}))
	defer server.Close()

	provider := &exaProvider{cfg: ExaConfig{
		BaseURL:           server.URL,
		APIKey:            "test-key",
		IncludeText:       true,
		TextMaxCharacters: 999,
	}}

	_, err := provider.Fetch(context.Background(), Request{URL: "https://example.com", MaxChars: 321})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text, ok := gotBody["text"].(map[string]any)
	if !ok {
		t.Fatalf("expected text object in payload, got %#v", gotBody["text"])
	}
	if int(text["maxCharacters"].(float64)) != 321 {
		t.Fatalf("expected maxCharacters=321, got %#v", text["maxCharacters"])
	}
}

func TestExaProviderFetchRespectsIncludeTextFalse(t *testing.T) {
	t.Helper()

	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"url":"https://example.com","summary":"summary text"}],"statuses":[{"id":"https://example.com","status":"success"}]}`))
	}))
	defer server.Close()

	provider := &exaProvider{cfg: ExaConfig{
		BaseURL:           server.URL,
		APIKey:            "test-key",
		IncludeText:       false,
		TextMaxCharacters: 999,
	}}

	resp, err := provider.Fetch(context.Background(), Request{URL: "https://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, exists := gotBody["text"]; exists {
		t.Fatalf("did not expect text payload when include_text=false: %#v", gotBody["text"])
	}
	if _, exists := gotBody["summary"]; !exists {
		t.Fatalf("expected summary payload when include_text=false")
	}
	if resp.Text != "summary text" {
		t.Fatalf("expected summary fallback text, got %q", resp.Text)
	}
}

func TestExaProviderFetchReturnsStatusErrors(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[],"statuses":[{"id":"https://example.com","status":"error","error":{"tag":"CRAWL_TIMEOUT","httpStatusCode":408}}]}`))
	}))
	defer server.Close()

	provider := &exaProvider{cfg: ExaConfig{
		BaseURL:           server.URL,
		APIKey:            "test-key",
		IncludeText:       true,
		TextMaxCharacters: 100,
	}}

	_, err := provider.Fetch(context.Background(), Request{URL: "https://example.com"})
	if err == nil {
		t.Fatalf("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "CRAWL_TIMEOUT") || !strings.Contains(msg, "408") {
		t.Fatalf("expected status details in error, got: %s", msg)
	}
}
