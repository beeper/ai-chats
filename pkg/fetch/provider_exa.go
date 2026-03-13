package fetch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/beeper/agentremote/pkg/shared/exa"
)

type exaProvider struct {
	cfg ExaConfig
}

func newExaProvider(cfg *Config) Provider {
	if cfg == nil {
		return nil
	}
	if !exa.Enabled(cfg.Exa.Enabled, cfg.Exa.APIKey) {
		return nil
	}
	return &exaProvider{cfg: cfg.Exa}
}

func (p *exaProvider) Name() string {
	return ProviderExa
}

func (p *exaProvider) Fetch(ctx context.Context, req Request) (*Response, error) {
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = p.cfg.TextMaxCharacters
	}
	payload := map[string]any{
		"urls": []string{req.URL},
	}
	if p.cfg.IncludeText || req.MaxChars > 0 {
		if maxChars > 0 {
			payload["text"] = map[string]any{"maxCharacters": maxChars}
		} else {
			payload["text"] = true
		}
	} else {
		payload["summary"] = map[string]any{}
	}

	start := time.Now()
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
	if err := exa.PostAndDecodeJSON(ctx, p.cfg.BaseURL, "/contents", p.cfg.APIKey, payload, DefaultTimeoutSecs, &resp); err != nil {
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

	// First, try to match the target URL specifically.
	// If matched but not an error, return empty (success).
	// If no URL match, fall back to the first error status.
	var matched *exaContentStatus
	var firstError *exaContentStatus
	for i := range statuses {
		s := &statuses[i]
		isError := strings.EqualFold(strings.TrimSpace(s.Status), "error")
		if strings.EqualFold(strings.TrimSpace(s.ID), targetURL) {
			if !isError {
				return ""
			}
			matched = s
			break
		}
		if isError && firstError == nil {
			firstError = s
		}
	}
	if matched == nil {
		matched = firstError
	}
	if matched == nil {
		return ""
	}
	tag := "unknown error"
	if matched.Error != nil {
		tag = strings.TrimSpace(matched.Error.Tag)
		if tag == "" {
			tag = "unknown_error"
		}
		if matched.Error.HTTPStatusCode != nil {
			tag = fmt.Sprintf("%s (http %d)", tag, *matched.Error.HTTPStatusCode)
		}
	}
	if matched.ID == "" {
		return tag
	}
	return fmt.Sprintf("%s: %s", matched.ID, tag)
}
