package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/beeper/ai-bridge/pkg/search"
	"github.com/beeper/ai-bridge/pkg/shared/exa"
	"github.com/beeper/ai-bridge/pkg/shared/httputil"
)

func executeBeeperDocs(ctx context.Context, args map[string]any) (string, error) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", errors.New("missing or invalid 'query' argument")
	}

	numResults := 5
	if c, ok := args["count"].(float64); ok && c >= 1 && c <= 10 {
		numResults = int(c)
	}

	btc := GetBridgeToolContext(ctx)
	var cfg *search.Config
	if btc != nil && btc.Client != nil {
		cfg = btc.Client.effectiveSearchConfig(ctx)
	}
	if cfg == nil || (cfg.Exa.APIKey == "" && cfg.Exa.BaseURL == "") {
		return "", errors.New("beeper_docs requires Exa search configuration")
	}

	apiKey := cfg.Exa.APIKey
	baseURL := cfg.Exa.BaseURL
	if baseURL == "" {
		baseURL = "https://api.exa.ai"
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/search"

	payload := map[string]any{
		"query":          query,
		"type":           "auto",
		"numResults":     numResults,
		"includeDomains": []string{"help.beeper.com", "developers.beeper.com"},
		"contents": map[string]any{
			"highlights": true,
			"extras": map[string]any{
				"links": 5,
			},
		},
	}

	data, _, err := httputil.PostJSON(ctx, endpoint, exa.AuthHeaders(baseURL, apiKey), payload, 30)
	if err != nil {
		return "", fmt.Errorf("beeper_docs search failed: %w", err)
	}

	var resp struct {
		Results []struct {
			Title      string   `json:"title"`
			URL        string   `json:"url"`
			Highlights []string `json:"highlights"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("beeper_docs: failed to parse response: %w", err)
	}

	type docResult struct {
		Title      string   `json:"title"`
		URL        string   `json:"url"`
		Highlights []string `json:"highlights,omitempty"`
	}

	results := make([]docResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, docResult{
			Title:      strings.TrimSpace(r.Title),
			URL:        r.URL,
			Highlights: r.Highlights,
		})
	}

	output := map[string]any{
		"query":   query,
		"count":   len(results),
		"results": results,
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("beeper_docs: failed to encode response: %w", err)
	}
	return string(raw), nil
}
