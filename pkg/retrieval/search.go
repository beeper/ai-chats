package retrieval

import (
	"context"
	"errors"
	"strings"

	"github.com/beeper/ai-chats/pkg/shared/stringutil"
)

// Search executes a search using the configured provider chain.
func Search(ctx context.Context, req SearchRequest, cfg *SearchConfig) (*SearchResponse, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, errors.New("missing query")
	}
	cfg = cfg.WithDefaults()
	if req.Count <= 0 {
		req.Count = DefaultSearchCount
	}
	if req.Count > MaxSearchCount {
		req.Count = MaxSearchCount
	}

	if !stringutil.BoolPtrOr(cfg.Exa.Enabled, true) || strings.TrimSpace(cfg.Exa.APIKey) == "" {
		return nil, errors.New("no search providers available")
	}
	resp, err := (&exaSearchProvider{cfg: cfg.Exa}).Search(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Provider == "" {
		resp.Provider = ProviderExa
	}
	if resp.Query == "" {
		resp.Query = req.Query
	}
	if resp.Count == 0 {
		resp.Count = len(resp.Results)
	}
	return resp, nil
}
