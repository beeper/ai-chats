package retrieval

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/beeper/agentremote/pkg/shared/exa"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

type exaSearchProvider struct {
	cfg ExaConfig
}

func newExaSearchProvider(cfg *SearchConfig) SearchProvider {
	if cfg == nil {
		return nil
	}
	if !stringutil.BoolPtrOr(cfg.Exa.Enabled, true) || strings.TrimSpace(cfg.Exa.APIKey) == "" {
		return nil
	}
	return &exaSearchProvider{cfg: cfg.Exa}
}

func (p *exaSearchProvider) Name() string {
	return ProviderExa
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
		desc := descriptionFromEntry(entry.Highlights, entry.Text)
		results = append(results, SearchResult{
			ID:          strings.TrimSpace(entry.ID),
			Title:       strings.TrimSpace(entry.Title),
			URL:         entry.URL,
			Description: desc,
			Published:   entry.PublishedDate,
			SiteName:    resolveSiteName(entry.URL),
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

func descriptionFromEntry(highlights []string, text string) string {
	if len(highlights) > 0 {
		return strings.TrimSpace(highlights[0])
	}
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > 240 {
		return trimmed[:240] + "..."
	}
	return trimmed
}

func resolveSiteName(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
