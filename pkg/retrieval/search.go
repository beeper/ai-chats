package retrieval

import (
	"context"
	"errors"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/providerresource"
	"github.com/beeper/agentremote/pkg/shared/registry"
)

// Search executes a search using the configured provider chain.
func Search(ctx context.Context, req SearchRequest, cfg *SearchConfig) (*SearchResponse, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, errors.New("missing query")
	}
	cfg = cfg.WithDefaults()
	req = normalizeSearchRequest(req)

	return providerresource.Run(
		cfg.Provider,
		cfg.Fallbacks,
		DefaultSearchFallbackOrder,
		func(reg *registry.Registry[SearchProvider]) {
			registerSearchProviders(reg, cfg)
		},
		func(provider SearchProvider) (*SearchResponse, error) {
			return provider.Search(ctx, req)
		},
		func(name string, resp *SearchResponse) {
			if resp.Provider == "" {
				resp.Provider = name
			}
			if resp.Query == "" {
				resp.Query = req.Query
			}
			if resp.Count == 0 {
				resp.Count = len(resp.Results)
			}
		},
		errors.New("no search providers available"),
	)
}

func normalizeSearchRequest(req SearchRequest) SearchRequest {
	if req.Count <= 0 {
		req.Count = DefaultSearchCount
	}
	if req.Count > MaxSearchCount {
		req.Count = MaxSearchCount
	}
	return req
}

func registerSearchProviders(reg *registry.Registry[SearchProvider], cfg *SearchConfig) {
	if p := newExaSearchProvider(cfg); p != nil {
		reg.Register(p)
	}
}
