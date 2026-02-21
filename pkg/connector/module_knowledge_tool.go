package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	integrationmemory "github.com/beeper/ai-bridge/pkg/integrations/memory"
)

type memorySearchOptions = integrationmemory.SearchOptions
type memorySearchResult = integrationmemory.SearchResult
type memoryFallbackStatus = integrationmemory.FallbackStatus

type memorySearchOutput struct {
	Results   []memorySearchResult  `json:"results"`
	Provider  string                `json:"provider,omitempty"`
	Model     string                `json:"model,omitempty"`
	Fallback  *memoryFallbackStatus `json:"fallback,omitempty"`
	Citations string                `json:"citations,omitempty"`
	Disabled  bool                  `json:"disabled,omitempty"`
	Error     string                `json:"error,omitempty"`
}

type memoryGetOutput struct {
	Path     string `json:"path"`
	Text     string `json:"text"`
	Disabled bool   `json:"disabled,omitempty"`
	Error    string `json:"error,omitempty"`
}

// executeMemorySearch handles the memory_search tool.
func executeMemorySearch(ctx context.Context, args map[string]any) (string, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil {
		return "", errors.New("memory_search requires bridge context")
	}
	var memoryModule *integrationmemory.Integration
	if btc.Client != nil {
		memoryModule = btc.Client.memoryModule()
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
	if btc.Client == nil || memoryModule == nil {
		payload := memorySearchOutput{
			Results:  []memorySearchResult{},
			Disabled: true,
			Error:    "memory integration unavailable",
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}
	manager, errMsg := memoryModule.GetManager(btc.Client.toolScope(btc.Portal, meta))
	if manager == nil {
		payload := memorySearchOutput{
			Results:  []memorySearchResult{},
			Disabled: true,
			Error:    errMsg,
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}

	opts := memorySearchOptions{
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
		payload := memorySearchOutput{
			Results:  []memorySearchResult{},
			Disabled: true,
			Error:    err.Error(),
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}

	status := manager.Status()
	citationsMode := resolveMemoryCitationsMode(btc.Client)
	includeCitations := shouldIncludeMemoryCitations(ctx, btc.Client, btc.Portal, citationsMode)
	decorated := decorateMemorySearchResults(results, includeCitations)
	payload := memorySearchOutput{
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

// executeMemoryGet handles the memory_get tool.
func executeMemoryGet(ctx context.Context, args map[string]any) (string, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil {
		return "", errors.New("memory_get requires bridge context")
	}
	var memoryModule *integrationmemory.Integration
	if btc.Client != nil {
		memoryModule = btc.Client.memoryModule()
	}

	pathRaw, ok := args["path"].(string)
	path := strings.TrimSpace(pathRaw)
	if !ok || path == "" {
		return "", errors.New("path required")
	}

	meta := portalMeta(btc.Portal)
	if btc.Client == nil || memoryModule == nil {
		payload := memoryGetOutput{
			Path:     path,
			Text:     "",
			Disabled: true,
			Error:    "memory integration unavailable",
		}
		output, _ := json.MarshalIndent(payload, "", "  ")
		return string(output), nil
	}
	manager, errMsg := memoryModule.GetManager(btc.Client.toolScope(btc.Portal, meta))
	if manager == nil {
		payload := memoryGetOutput{
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
		payload := memoryGetOutput{
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
	payload := memoryGetOutput{Path: resolvedPath, Text: text}
	output, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("couldn't format the result: %w", err)
	}

	return string(output), nil
}

func resolveMemoryCitationsMode(client *AIClient) string {
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

func shouldIncludeMemoryCitations(ctx context.Context, client *AIClient, portal *bridgev2.Portal, mode string) bool {
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

func decorateMemorySearchResults(results []memorySearchResult, include bool) []memorySearchResult {
	if !include || len(results) == 0 {
		return results
	}
	out := make([]memorySearchResult, 0, len(results))
	for _, entry := range results {
		next := entry
		citation := formatMemoryCitation(entry)
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

func formatMemoryCitation(entry memorySearchResult) string {
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
