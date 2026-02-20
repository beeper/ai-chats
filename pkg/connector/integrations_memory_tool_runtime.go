package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
)

type recallSearchOutput struct {
	Results   []recallSearchResult  `json:"results"`
	Provider  string                `json:"provider,omitempty"`
	Model     string                `json:"model,omitempty"`
	Fallback  *recallFallbackStatus `json:"fallback,omitempty"`
	Citations string                `json:"citations,omitempty"`
	Disabled  bool                  `json:"disabled,omitempty"`
	Error     string                `json:"error,omitempty"`
}

type recallGetOutput struct {
	Path     string `json:"path"`
	Text     string `json:"text"`
	Disabled bool   `json:"disabled,omitempty"`
	Error    string `json:"error,omitempty"`
}

// executeRecallSearch handles the memory_search tool.
func executeRecallSearch(ctx context.Context, args map[string]any) (string, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil {
		return "", errors.New("memory_search requires bridge context")
	}

	mode := ""
	if raw, ok := args["mode"].(string); ok {
		mode = strings.ToLower(strings.TrimSpace(raw))
	}
	query := ""
	if raw, ok := args["query"].(string); ok {
		query = strings.TrimSpace(raw)
	}
	if mode != "list" && query == "" {
		return "", errors.New("query required")
	}
	var maxResults *int
	var minScore *float64

	if raw := args["maxResults"]; raw != nil {
		if max, ok := readNumberArg(raw); ok {
			val := int(max)
			maxResults = &val
		}
	}
	if raw := args["minScore"]; raw != nil {
		if score, ok := readNumberArg(raw); ok {
			minScore = &score
		}
	}

	meta := portalMeta(btc.Portal)
	if btc.Client == nil || btc.Client.recallIntegration == nil {
		payload := recallSearchOutput{
			Results:  []recallSearchResult{},
			Disabled: true,
			Error:    "recall integration unavailable",
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}
	manager, errMsg := btc.Client.recallIntegration.GetManager(btc.Client.toolScope(btc.Portal, meta))
	if manager == nil {
		payload := recallSearchOutput{
			Results:  []recallSearchResult{},
			Disabled: true,
			Error:    errMsg,
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}

	opts := recallSearchOptions{
		SessionKey: btc.Portal.PortalKey.String(),
		MinScore:   math.NaN(),
		Mode:       mode,
	}
	if maxResults != nil {
		opts.MaxResults = *maxResults
	}
	if minScore != nil {
		opts.MinScore = *minScore
	}
	if raw, ok := args["pathPrefix"].(string); ok {
		opts.PathPrefix = strings.TrimSpace(raw)
	}
	if raw := args["sources"]; raw != nil {
		if list, ok := raw.([]any); ok {
			out := make([]string, 0, len(list))
			for _, item := range list {
				if s, ok := item.(string); ok {
					if trimmed := strings.TrimSpace(s); trimmed != "" {
						out = append(out, trimmed)
					}
				}
			}
			if len(out) > 0 {
				opts.Sources = out
			}
		} else if list, ok := raw.([]string); ok {
			out := make([]string, 0, len(list))
			for _, s := range list {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					out = append(out, trimmed)
				}
			}
			if len(out) > 0 {
				opts.Sources = out
			}
		}
	}
	searchCtx, searchCancel := context.WithTimeout(ctx, memorySearchTimeout)
	defer searchCancel()
	results, err := manager.Search(searchCtx, query, opts)
	if err != nil {
		payload := recallSearchOutput{
			Results:  []recallSearchResult{},
			Disabled: true,
			Error:    err.Error(),
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}

	status := manager.Status()
	citationsMode := resolveRecallCitationsMode(btc.Client)
	includeCitations := shouldIncludeRecallCitations(ctx, btc.Client, btc.Portal, citationsMode)
	decorated := decorateRecallSearchResults(results, includeCitations)
	payload := recallSearchOutput{
		Results:   decorated,
		Provider:  status.Provider,
		Model:     status.Model,
		Fallback:  status.Fallback,
		Citations: citationsMode,
	}
	output, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("couldn't format results: %w", err)
	}

	return string(output), nil
}

// executeRecallGet handles the memory_get tool.
func executeRecallGet(ctx context.Context, args map[string]any) (string, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil {
		return "", errors.New("memory_get requires bridge context")
	}

	pathRaw, ok := args["path"].(string)
	path := strings.TrimSpace(pathRaw)
	if !ok || path == "" {
		return "", errors.New("path required")
	}

	meta := portalMeta(btc.Portal)
	if btc.Client == nil || btc.Client.recallIntegration == nil {
		payload := recallGetOutput{
			Path:     path,
			Text:     "",
			Disabled: true,
			Error:    "recall integration unavailable",
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}
	manager, errMsg := btc.Client.recallIntegration.GetManager(btc.Client.toolScope(btc.Portal, meta))
	if manager == nil {
		payload := recallGetOutput{
			Path:     path,
			Text:     "",
			Disabled: true,
			Error:    errMsg,
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}

	var from *int
	var lines *int
	if raw := args["from"]; raw != nil {
		if value, ok := readNumberArg(raw); ok {
			val := int(value)
			from = &val
		}
	}
	if raw := args["lines"]; raw != nil {
		if value, ok := readNumberArg(raw); ok {
			val := int(value)
			lines = &val
		}
	}

	result, err := manager.ReadFile(ctx, path, from, lines)
	if err != nil {
		payload := recallGetOutput{
			Path:     path,
			Text:     "",
			Disabled: true,
			Error:    err.Error(),
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}
	text, _ := result["text"].(string)
	resolvedPath, _ := result["path"].(string)
	if resolvedPath == "" {
		resolvedPath = path
	}
	payload := recallGetOutput{
		Path: resolvedPath,
		Text: text,
	}
	output, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("couldn't format the result: %w", err)
	}

	return string(output), nil
}

func resolveRecallCitationsMode(client *AIClient) string {
	if client == nil || client.connector == nil || client.connector.Config.Memory == nil {
		return "auto"
	}
	mode := strings.ToLower(strings.TrimSpace(client.connector.Config.Memory.Citations))
	switch mode {
	case "on", "off", "auto":
		return mode
	default:
		return "auto"
	}
}

func shouldIncludeRecallCitations(ctx context.Context, client *AIClient, portal *bridgev2.Portal, mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on":
		return true
	case "off":
		return false
	default:
	}
	if client == nil || portal == nil {
		return true
	}
	return !client.isGroupChat(ctx, portal)
}

func decorateRecallSearchResults(results []recallSearchResult, include bool) []recallSearchResult {
	if !include || len(results) == 0 {
		return results
	}
	out := make([]recallSearchResult, 0, len(results))
	for _, entry := range results {
		next := entry
		citation := formatRecallCitation(entry)
		if citation != "" {
			snippet := strings.TrimSpace(entry.Snippet)
			if snippet != "" {
				next.Snippet = fmt.Sprintf("%s\n\nSource: %s", snippet, citation)
			} else {
				next.Snippet = fmt.Sprintf("Source: %s", citation)
			}
		}
		out = append(out, next)
	}
	return out
}

func formatRecallCitation(entry recallSearchResult) string {
	if strings.TrimSpace(entry.Path) == "" {
		return ""
	}
	if entry.StartLine > 0 && entry.EndLine > 0 {
		if entry.StartLine == entry.EndLine {
			return fmt.Sprintf("%s#L%d", entry.Path, entry.StartLine)
		}
		return fmt.Sprintf("%s#L%d-L%d", entry.Path, entry.StartLine, entry.EndLine)
	}
	return entry.Path
}
