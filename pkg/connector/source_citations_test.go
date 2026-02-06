package connector

import "testing"

func TestExtractWebSearchCitationsFromToolOutput(t *testing.T) {
	output := `{
		"query":"open source llm",
		"provider":"exa",
		"results":[
			{"title":"One","url":"https://example.com/one","description":"a"},
			{"title":"Two","url":"http://example.org/two","description":"b"},
			{"title":"Skip","url":"ftp://example.net/three"}
		]
	}`

	got := extractWebSearchCitationsFromToolOutput("web_search", output)
	if len(got) != 2 {
		t.Fatalf("expected 2 citations, got %d", len(got))
	}
	if got[0].URL != "https://example.com/one" || got[0].Title != "One" {
		t.Fatalf("unexpected first citation: %+v", got[0])
	}
	if got[1].URL != "http://example.org/two" || got[1].Title != "Two" {
		t.Fatalf("unexpected second citation: %+v", got[1])
	}
}

func TestExtractWebSearchCitationsFromToolOutput_OnlyForWebSearch(t *testing.T) {
	output := `{"results":[{"title":"One","url":"https://example.com/one"}]}`
	got := extractWebSearchCitationsFromToolOutput("web_fetch", output)
	if len(got) != 0 {
		t.Fatalf("expected no citations for non-web_search tool, got %d", len(got))
	}
}

func TestMergeSourceCitations_DedupesByURL(t *testing.T) {
	existing := []sourceCitation{
		{URL: "https://example.com/one", Title: "One"},
	}
	incoming := []sourceCitation{
		{URL: "https://example.com/one", Title: "Duplicate"},
		{URL: "https://example.com/two", Title: "Two"},
	}

	got := mergeSourceCitations(existing, incoming)
	if len(got) != 2 {
		t.Fatalf("expected 2 merged citations, got %d", len(got))
	}
	if got[0].URL != "https://example.com/one" {
		t.Fatalf("unexpected first merged citation: %+v", got[0])
	}
	if got[1].URL != "https://example.com/two" {
		t.Fatalf("unexpected second merged citation: %+v", got[1])
	}
}
