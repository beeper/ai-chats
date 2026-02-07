package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/shared/exa"
)

type exaProvider struct {
	cfg ExaConfig
}

func newExaProvider(cfg *Config) Provider {
	if cfg == nil {
		return nil
	}
	if !isEnabled(cfg.Exa.Enabled, true) {
		return nil
	}
	apiKey := strings.TrimSpace(cfg.Exa.APIKey)
	if apiKey == "" {
		return nil
	}
	return &exaProvider{cfg: cfg.Exa}
}

func (p *exaProvider) Name() string {
	return ProviderExa
}

func (p *exaProvider) Fetch(ctx context.Context, req Request) (*Response, error) {
	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + "/contents"
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = p.cfg.TextMaxCharacters
	}
	payload := map[string]any{
		"urls": []string{req.URL},
	}
	includeText := p.cfg.IncludeText || req.MaxChars > 0
	if includeText {
		if maxChars > 0 {
			payload["text"] = map[string]any{
				"maxCharacters": maxChars,
			}
		} else {
			payload["text"] = true
		}
	} else {
		// Keep fetch useful when text is disabled in config.
		payload["summary"] = map[string]any{}
	}

	start := time.Now()
	data, _, err := postJSON(ctx, endpoint, exaAuthHeaders(p.cfg.BaseURL, p.cfg.APIKey), payload, DefaultTimeoutSecs)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Results []struct {
			URL         string   `json:"url"`
			Text        string   `json:"text"`
			Summary     string   `json:"summary"`
			Highlights  []string `json:"highlights"`
			Title       string   `json:"title"`
			PublishedAt string   `json:"publishedDate"`
		} `json:"results"`
		Statuses    []exaContentStatus `json:"statuses"`
		CostDollars map[string]any     `json:"costDollars"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	statusErr := formatExaStatusError(req.URL, resp.Statuses)
	if len(resp.Results) == 0 {
		if statusErr != "" {
			return nil, fmt.Errorf("exa contents status error: %s", statusErr)
		}
		return nil, errors.New("exa contents returned no results")
	}
	entry := resp.Results[0]
	text := entry.Text
	if text == "" && len(entry.Highlights) > 0 {
		text = entry.Highlights[0]
	}
	if text == "" {
		text = entry.Summary
	}
	if text == "" && statusErr != "" {
		return nil, fmt.Errorf("exa contents status error: %s", statusErr)
	}
	length := len(text)
	finalURL := req.URL
	if strings.TrimSpace(entry.URL) != "" {
		finalURL = entry.URL
	}
	return &Response{
		URL:           req.URL,
		FinalURL:      finalURL,
		Status:        200,
		ContentType:   "text/plain",
		ExtractMode:   req.ExtractMode,
		Extractor:     "exa-contents",
		Truncated:     length >= req.MaxChars && req.MaxChars > 0,
		Length:        length,
		RawLength:     length,
		WrappedLength: length,
		FetchedAt:     time.Now().UTC().Format(time.RFC3339),
		TookMs:        time.Since(start).Milliseconds(),
		Text:          text,
		Provider:      ProviderExa,
		Extras: map[string]any{
			"costDollars": resp.CostDollars,
			"statuses":    resp.Statuses,
		},
	}, nil
}

type exaContentStatus struct {
	ID     string         `json:"id"`
	Status string         `json:"status"`
	Error  *exaStatusInfo `json:"error"`
}

type exaStatusInfo struct {
	Tag            string `json:"tag"`
	HTTPStatusCode *int   `json:"httpStatusCode"`
}

func formatExaStatusError(targetURL string, statuses []exaContentStatus) string {
	if len(statuses) == 0 {
		return ""
	}

	targetURL = strings.TrimSpace(targetURL)
	var matched *exaContentStatus
	for i := range statuses {
		status := statuses[i]
		if strings.EqualFold(strings.TrimSpace(status.ID), targetURL) {
			if !strings.EqualFold(strings.TrimSpace(status.Status), "error") {
				return ""
			}
			matched = &status
			break
		}
	}
	if matched == nil {
		for i := range statuses {
			status := statuses[i]
			if strings.EqualFold(strings.TrimSpace(status.Status), "error") {
				matched = &status
				break
			}
		}
	}
	if matched == nil {
		return ""
	}
	if matched.Error == nil {
		if matched.ID == "" {
			return "unknown error"
		}
		return fmt.Sprintf("%s: unknown error", matched.ID)
	}

	tag := strings.TrimSpace(matched.Error.Tag)
	if tag == "" {
		tag = "unknown_error"
	}
	if matched.Error.HTTPStatusCode != nil {
		if matched.ID == "" {
			return fmt.Sprintf("%s (http %d)", tag, *matched.Error.HTTPStatusCode)
		}
		return fmt.Sprintf("%s: %s (http %d)", matched.ID, tag, *matched.Error.HTTPStatusCode)
	}
	if matched.ID == "" {
		return tag
	}
	return fmt.Sprintf("%s: %s", matched.ID, tag)
}

var exaAuthHeaders = exa.AuthHeaders
