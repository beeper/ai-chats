package search

import (
	"context"
	"errors"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

// Search executes a search using the configured provider chain.
func Search(ctx context.Context, req Request, cfg *Config) (*Response, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, errors.New("missing query")
	}
	cfg = cfg.WithDefaults()
	req = normalizeRequest(req)

	provider, name := resolveProvider(cfg)
	if provider == nil {
		return nil, errors.New("no search providers available")
	}
	resp, err := provider.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Provider == "" {
		resp.Provider = name
	}
	if resp.Query == "" {
		resp.Query = req.Query
	}
	if resp.Count == 0 {
		resp.Count = len(resp.Results)
	}
	return resp, nil
}

func normalizeRequest(req Request) Request {
	if req.Count <= 0 {
		req.Count = DefaultSearchCount
	}
	if req.Count > MaxSearchCount {
		req.Count = MaxSearchCount
	}
	return req
}

func resolveProvider(cfg *Config) (*exaProvider, string) {
	order := stringutil.BuildProviderOrder(cfg.Provider, cfg.Fallbacks, DefaultFallbackOrder)
	for _, name := range order {
		if strings.EqualFold(name, ProviderExa) {
			if provider := newExaProvider(cfg); provider != nil {
				return provider, ProviderExa
			}
		}
	}
	return nil, ""
}
