package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/beeper/agentremote/pkg/retrieval"
	"github.com/beeper/agentremote/pkg/shared/exa"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/pkg/shared/toolspec"
	"github.com/beeper/agentremote/pkg/shared/websearch"
)

// WebSearch is the web search tool definition.
var WebSearch = newBuiltinTool(
	toolspec.WebSearchName,
	toolspec.WebSearchDescription,
	"Web Search",
	toolspec.WebSearchSchema(),
	GroupSearch,
	executeWebSearch,
)

// executeWebSearch performs a web search using the configured providers.
func executeWebSearch(ctx context.Context, args map[string]any) (*Result, error) {
	req, err := websearch.RequestFromArgs(args)
	if err != nil {
		return ErrorResult("web_search", err.Error()), nil
	}

	cfg := &retrieval.SearchConfig{}
	cfg.Provider = stringutil.EnvOr(cfg.Provider, os.Getenv("SEARCH_PROVIDER"))
	if len(cfg.Fallbacks) == 0 {
		if raw := strings.TrimSpace(os.Getenv("SEARCH_FALLBACKS")); raw != "" {
			cfg.Fallbacks = stringutil.SplitCSV(raw)
		}
	}
	exa.ApplyEnv(&cfg.Exa.APIKey, &cfg.Exa.BaseURL)
	cfg = cfg.WithDefaults()
	searchReq := retrieval.SearchRequest(req)
	resp, err := retrieval.Search(ctx, searchReq, cfg)
	if err != nil {
		return ErrorResult("web_search", fmt.Sprintf("search failed: %v", err)), nil
	}

	return JSONResult(websearch.PayloadFromResponse(resp)), nil
}
