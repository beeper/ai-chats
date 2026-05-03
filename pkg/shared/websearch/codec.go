package websearch

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/beeper/ai-chats/pkg/retrieval"
	"github.com/beeper/ai-chats/pkg/shared/maputil"
)

// RequestFromArgs converts tool arguments into a normalized search request.
func RequestFromArgs(args map[string]any) (retrieval.SearchRequest, error) {
	query := maputil.StringArg(args, "query")
	if query == "" {
		return retrieval.SearchRequest{}, errors.New("missing or invalid 'query' argument")
	}
	count, _ := ParseCountAndIgnoredOptions(args)

	return retrieval.SearchRequest{
		Query:      query,
		Count:      count,
		Country:    maputil.StringArg(args, "country"),
		SearchLang: maputil.StringArg(args, "search_lang"),
		UILang:     maputil.StringArg(args, "ui_lang"),
		Freshness:  maputil.StringArg(args, "freshness"),
	}, nil
}

// PayloadFromResponse converts a normalized search response into the common JSON payload shape.
// Only non-zero fields are included to keep the payload compact.
func PayloadFromResponse(resp *retrieval.SearchResponse) map[string]any {
	payload := map[string]any{
		"query":    resp.Query,
		"provider": resp.Provider,
		"count":    resp.Count,
	}
	if resp.TookMs > 0 {
		payload["tookMs"] = resp.TookMs
	}
	if resp.Answer != "" {
		payload["answer"] = resp.Answer
	}
	if resp.Summary != "" {
		payload["summary"] = resp.Summary
	}
	if resp.Definition != "" {
		payload["definition"] = resp.Definition
	}
	if resp.Warning != "" {
		payload["warning"] = resp.Warning
	}
	if resp.NoResults {
		payload["noResults"] = true
	}
	if resp.Cached {
		payload["cached"] = true
	}

	if len(resp.Results) > 0 {
		results := make([]map[string]any, 0, len(resp.Results))
		for _, r := range resp.Results {
			entry := map[string]any{
				"title":       r.Title,
				"url":         r.URL,
				"description": r.Description,
				"published":   r.Published,
				"siteName":    r.SiteName,
			}
			if r.ID != "" {
				entry["id"] = r.ID
			}
			if r.Author != "" {
				entry["author"] = r.Author
			}
			if r.Image != "" {
				entry["image"] = r.Image
			}
			if r.Favicon != "" {
				entry["favicon"] = r.Favicon
			}
			results = append(results, entry)
		}
		payload["results"] = results
	}

	if resp.Extras != nil {
		payload["extras"] = resp.Extras
	}
	return payload
}

// ResultsFromPayload extracts search results from the common payload map.
func ResultsFromPayload(payload map[string]any) []retrieval.SearchResult {
	raw, ok := payload["results"]
	if !ok {
		return nil
	}
	// After JSON round-tripping, results arrive as []any; when called
	// directly with PayloadFromResponse output, they are []map[string]any.
	var entries []map[string]any
	switch v := raw.(type) {
	case []any:
		for _, item := range v {
			if entry, ok := item.(map[string]any); ok {
				entries = append(entries, entry)
			}
		}
	case []map[string]any:
		entries = v
	}
	if len(entries) == 0 {
		return nil
	}
	results := make([]retrieval.SearchResult, 0, len(entries))
	for _, entry := range entries {
		results = append(results, resultFromMap(entry))
	}
	return results
}

// ResultsFromJSON extracts search results from a JSON-encoded payload.
func ResultsFromJSON(output string) []retrieval.SearchResult {
	output = strings.TrimSpace(output)
	if output == "" || !strings.HasPrefix(output, "{") {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil
	}
	return ResultsFromPayload(payload)
}

func resultFromMap(entry map[string]any) retrieval.SearchResult {
	return retrieval.SearchResult{
		ID:          maputil.StringArg(entry, "id"),
		Title:       maputil.StringArg(entry, "title"),
		URL:         maputil.StringArg(entry, "url"),
		Description: maputil.StringArg(entry, "description"),
		Published:   maputil.StringArg(entry, "published"),
		SiteName:    maputil.StringArg(entry, "siteName"),
		Author:      maputil.StringArg(entry, "author"),
		Image:       maputil.StringArg(entry, "image"),
		Favicon:     maputil.StringArg(entry, "favicon"),
	}
}
