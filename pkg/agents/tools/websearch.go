package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/beeper/ai-bridge/pkg/search"
	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
	"github.com/beeper/ai-bridge/pkg/shared/websearch"
)

// WebSearch is the web search tool definition.
var WebSearch = &Tool{
	Tool: mcp.Tool{
		Name:        toolspec.WebSearchName,
		Description: toolspec.WebSearchDescription,
		Annotations: &mcp.ToolAnnotations{Title: "Web Search"},
		InputSchema: toolspec.WebSearchSchema(),
	},
	Type:    ToolTypeBuiltin,
	Group:   GroupSearch,
	Execute: executeWebSearch,
}

// executeWebSearch performs a web search using the configured providers.
func executeWebSearch(ctx context.Context, args map[string]any) (*Result, error) {
	query, err := ReadString(args, "query", true)
	if err != nil {
		return ErrorResult("web_search", err.Error()), nil
	}
	count, _ := websearch.ParseCountAndIgnoredOptions(args)
	country, _ := args["country"].(string)
	searchLang, _ := args["search_lang"].(string)
	uiLang, _ := args["ui_lang"].(string)
	freshness, _ := args["freshness"].(string)

	req := search.Request{
		Query:      query,
		Count:      count,
		Country:    strings.TrimSpace(country),
		SearchLang: strings.TrimSpace(searchLang),
		UILang:     strings.TrimSpace(uiLang),
		Freshness:  strings.TrimSpace(freshness),
	}

	cfg := search.ApplyEnvDefaults(nil)
	resp, err := search.Search(ctx, req, cfg)
	if err != nil {
		return ErrorResult("web_search", fmt.Sprintf("search failed: %v", err)), nil
	}

	payload := map[string]any{
		"query":      resp.Query,
		"provider":   resp.Provider,
		"count":      resp.Count,
		"tookMs":     resp.TookMs,
		"answer":     resp.Answer,
		"summary":    resp.Summary,
		"definition": resp.Definition,
		"warning":    resp.Warning,
		"noResults":  resp.NoResults,
		"cached":     resp.Cached,
	}
	if len(resp.Results) > 0 {
		results := make([]map[string]any, 0, len(resp.Results))
		for _, r := range resp.Results {
			results = append(results, map[string]any{
				"title":       r.Title,
				"url":         r.URL,
				"description": r.Description,
				"published":   r.Published,
				"siteName":    r.SiteName,
			})
		}
		payload["results"] = results
	}
	if resp.Extras != nil {
		payload["extras"] = resp.Extras
	}

	return JSONResult(payload), nil
}
