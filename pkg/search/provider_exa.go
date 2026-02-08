package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/shared/exa"
	"github.com/beeper/ai-bridge/pkg/shared/httputil"
)

type exaProvider struct {
	cfg ExaConfig
}

func (p *exaProvider) Name() string {
	return ProviderExa
}

func (p *exaProvider) Search(ctx context.Context, req Request) (*Response, error) {
	endpoint := resolveEndpoint(p.cfg.BaseURL, "/search")
	if endpoint == "" {
		return nil, errors.New("exa base_url is empty")
	}
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
			highlightMaxChars := p.cfg.TextMaxCharacters
			if highlightMaxChars <= 0 {
				highlightMaxChars = 500
			}
			contents["highlights"] = map[string]any{
				"maxCharacters": highlightMaxChars,
			}
		}
		payload["contents"] = contents
	}

	start := time.Now()
	data, _, err := httputil.PostJSON(ctx, endpoint, exa.AuthHeaders(p.cfg.BaseURL, p.cfg.APIKey), payload, DefaultTimeoutSecs)
	if err != nil {
		return nil, err
	}

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
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(resp.Results))
	for _, entry := range resp.Results {
		desc := ""
		if len(entry.Highlights) > 0 {
			desc = strings.TrimSpace(entry.Highlights[0])
		} else if entry.Text != "" {
			desc = truncate(entry.Text, 240)
		}
		results = append(results, Result{
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

	return &Response{
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

func resolveEndpoint(baseURL, path string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return strings.TrimRight(trimmed, "/") + path
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = path
		return parsed.String()
	}
	return strings.TrimRight(trimmed, "/") + path
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func resolveSiteName(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
