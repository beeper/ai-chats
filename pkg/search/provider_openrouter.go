package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type openRouterProvider struct {
	cfg OpenRouterConfig
}

type openRouterAnnotation struct {
	Type        string `json:"type"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	URLCitation *struct {
		URL     string `json:"url"`
		Title   string `json:"title"`
		Content string `json:"content"`
	} `json:"url_citation"`
}

func newOpenRouterProvider(cfg *Config) Provider {
	if cfg == nil {
		return nil
	}
	enabled := isEnabled(cfg.OpenRouter.Enabled, true)
	if !enabled {
		return nil
	}
	apiKey := strings.TrimSpace(cfg.OpenRouter.APIKey)
	if apiKey == "" {
		return nil
	}
	return &openRouterProvider{cfg: cfg.OpenRouter}
}

func (p *openRouterProvider) Name() string {
	return ProviderOpenRouter
}

func (p *openRouterProvider) Search(ctx context.Context, req Request) (*Response, error) {
	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + "/chat/completions"
	payload := map[string]any{
		"model": p.cfg.Model,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": req.Query,
			},
		},
		"plugins": []map[string]any{
			{
				"id":          "web",
				"max_results": clampCount(req.Count),
			},
		},
	}
	start := time.Now()
	data, _, err := postJSON(ctx, endpoint, map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", p.cfg.APIKey),
	}, payload, p.cfg.TimeoutSecs)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Content     string                 `json:"content"`
				Annotations []openRouterAnnotation `json:"annotations"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	answer := ""
	results := []Result{}
	if len(resp.Choices) > 0 {
		message := resp.Choices[0].Message
		answer = strings.TrimSpace(message.Content)
		results = parseOpenRouterAnnotations(message.Annotations)
	}

	return &Response{
		Query:     req.Query,
		Provider:  ProviderOpenRouter,
		Count:     len(results),
		TookMs:    time.Since(start).Milliseconds(),
		Results:   results,
		Answer:    answer,
		NoResults: len(results) == 0 && answer == "",
	}, nil
}

func clampCount(value int) int {
	if value <= 0 {
		return DefaultSearchCount
	}
	if value > MaxSearchCount {
		return MaxSearchCount
	}
	return value
}

func parseOpenRouterAnnotations(annotations []openRouterAnnotation) []Result {
	if len(annotations) == 0 {
		return nil
	}
	results := make([]Result, 0, len(annotations))
	seen := make(map[string]struct{}, len(annotations))
	for _, ann := range annotations {
		if ann.Type != "" && ann.Type != "url_citation" {
			continue
		}
		urlValue := ""
		title := ""
		content := ""
		if ann.URLCitation != nil {
			urlValue = strings.TrimSpace(ann.URLCitation.URL)
			title = strings.TrimSpace(ann.URLCitation.Title)
			content = strings.TrimSpace(ann.URLCitation.Content)
		}
		if urlValue == "" {
			urlValue = strings.TrimSpace(ann.URL)
		}
		if title == "" {
			title = strings.TrimSpace(ann.Title)
		}
		if content == "" {
			content = strings.TrimSpace(ann.Content)
		}
		if urlValue == "" && title == "" {
			continue
		}
		if urlValue != "" {
			if _, ok := seen[urlValue]; ok {
				continue
			}
			seen[urlValue] = struct{}{}
		}
		if title == "" {
			title = urlValue
		}
		description := ""
		if content != "" {
			description = truncate(content, 240)
		}
		results = append(results, Result{
			Title:       title,
			URL:         urlValue,
			Description: description,
			SiteName:    resolveSiteName(urlValue),
		})
	}
	return results
}
