package retrieval

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/beeper/agentremote/pkg/shared/exa"
)

type exaSearchProvider struct {
	cfg ExaConfig
}

func (p *exaSearchProvider) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	numResults := p.cfg.NumResults
	if req.Count > 0 {
		numResults = req.Count
	}

	payload := map[string]any{
		"query":      req.Query,
		"type":       p.cfg.Type,
		"numResults": numResults,
	}
	if p.cfg.Category != "" {
		payload["category"] = p.cfg.Category
	}
	if req.Country != "" {
		payload["userLocation"] = strings.ToUpper(req.Country)
	}

	if p.cfg.IncludeText || p.cfg.Highlights {
		contents := map[string]any{}
		if p.cfg.IncludeText {
			if p.cfg.TextMaxCharacters > 0 {
				contents["text"] = map[string]any{"maxCharacters": p.cfg.TextMaxCharacters}
			} else {
				contents["text"] = true
			}
		}
		if p.cfg.Highlights {
			contents["highlights"] = map[string]any{
				"maxCharacters": p.cfg.TextMaxCharacters,
			}
		}
		payload["contents"] = contents
	}

	start := time.Now()
	var resp struct {
		Results []struct {
			ID            string   `json:"id"`
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			Author        string   `json:"author"`
			PublishedDate string   `json:"publishedDate"`
			Image         string   `json:"image"`
			Favicon       string   `json:"favicon"`
			Text          string   `json:"text"`
			Highlights    []string `json:"highlights"`
		} `json:"results"`
		CostDollars map[string]any `json:"costDollars"`
	}
	if err := exa.PostAndDecodeJSON(ctx, p.cfg.BaseURL, "/search", p.cfg.APIKey, payload, DefaultTimeoutSecs, &resp); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(resp.Results))
	for _, entry := range resp.Results {
		desc := strings.TrimSpace(entry.Text)
		if len(entry.Highlights) > 0 {
			desc = strings.TrimSpace(entry.Highlights[0])
		} else if len(desc) > 240 {
			desc = desc[:240] + "..."
		}
		siteName := ""
		if parsed, err := url.Parse(strings.TrimSpace(entry.URL)); err == nil {
			siteName = parsed.Hostname()
		}
		results = append(results, SearchResult{
			ID:          strings.TrimSpace(entry.ID),
			Title:       strings.TrimSpace(entry.Title),
			URL:         entry.URL,
			Description: desc,
			Published:   entry.PublishedDate,
			SiteName:    siteName,
			Author:      strings.TrimSpace(entry.Author),
			Image:       strings.TrimSpace(entry.Image),
			Favicon:     strings.TrimSpace(entry.Favicon),
		})
	}

	return &SearchResponse{
		Query:    req.Query,
		Provider: ProviderExa,
		Count:    len(results),
		TookMs:   time.Since(start).Milliseconds(),
		Results:  results,
		Extras: map[string]any{
			"costDollars": resp.CostDollars,
		},
		NoResults: len(results) == 0,
	}, nil
}
