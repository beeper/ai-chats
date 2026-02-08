package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/shared/httputil"
)

type perplexityProvider struct {
	cfg PerplexityConfig
}

func (p *perplexityProvider) Name() string {
	return ProviderPerplexity
}

func (p *perplexityProvider) Search(ctx context.Context, req Request) (*Response, error) {
	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + "/chat/completions"
	payload := map[string]any{
		"model": p.cfg.Model,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": req.Query,
			},
		},
	}
	start := time.Now()
	data, _, err := httputil.PostJSON(ctx, endpoint, map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", p.cfg.APIKey),
	}, payload, p.cfg.TimeoutSecs)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Citations []string `json:"citations"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	answer := ""
	if len(resp.Choices) > 0 {
		answer = strings.TrimSpace(resp.Choices[0].Message.Content)
	}
	results := make([]Result, 0, len(resp.Citations))
	for _, citation := range resp.Citations {
		results = append(results, Result{
			Title:    citation,
			URL:      citation,
			SiteName: resolveSiteName(citation),
		})
	}

	return &Response{
		Query:     req.Query,
		Provider:  ProviderPerplexity,
		Count:     len(results),
		TookMs:    time.Since(start).Milliseconds(),
		Results:   results,
		Answer:    answer,
		NoResults: len(results) == 0 && answer == "",
	}, nil
}
