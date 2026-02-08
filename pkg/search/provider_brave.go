package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/shared/httputil"
)

type braveProvider struct {
	cfg BraveConfig
}

func (p *braveProvider) Name() string {
	return ProviderBrave
}

func (p *braveProvider) Search(ctx context.Context, req Request) (*Response, error) {
	if p.cfg.BaseURL == "" {
		return nil, errors.New("brave base_url is empty")
	}
	searchURL, err := url.Parse(p.cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	count := req.Count
	if count <= 0 {
		count = DefaultSearchCount
	}
	queryValues := searchURL.Query()
	queryValues.Set("q", req.Query)
	queryValues.Set("count", fmt.Sprintf("%d", count))

	country := req.Country
	if country == "" {
		country = p.cfg.DefaultCountry
	}
	if country != "" {
		queryValues.Set("country", country)
	}
	searchLang := req.SearchLang
	if searchLang == "" {
		searchLang = p.cfg.SearchLang
	}
	if searchLang != "" {
		queryValues.Set("search_lang", searchLang)
	}
	uiLang := req.UILang
	if uiLang == "" {
		uiLang = p.cfg.UILang
	}
	if uiLang != "" {
		queryValues.Set("ui_lang", uiLang)
	}
	freshness := req.Freshness
	if freshness == "" {
		freshness = p.cfg.DefaultFreshness
	}
	if freshness != "" {
		queryValues.Set("freshness", freshness)
	}
	searchURL.RawQuery = queryValues.Encode()

	start := time.Now()
	data, _, err := httputil.GetJSON(ctx, searchURL.String(), map[string]string{
		"Accept":               "application/json",
		"X-Subscription-Token": p.cfg.APIKey,
	}, p.cfg.TimeoutSecs)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				Age         string `json:"age"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(resp.Web.Results))
	for _, entry := range resp.Web.Results {
		results = append(results, Result{
			Title:       strings.TrimSpace(entry.Title),
			URL:         entry.URL,
			Description: strings.TrimSpace(entry.Description),
			Published:   entry.Age,
			SiteName:    resolveSiteName(entry.URL),
		})
	}

	return &Response{
		Query:     req.Query,
		Provider:  ProviderBrave,
		Count:     len(results),
		TookMs:    time.Since(start).Milliseconds(),
		Results:   results,
		NoResults: len(results) == 0,
	}, nil
}
