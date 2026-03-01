package search

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

// Search executes a search using the configured provider chain.
func Search(ctx context.Context, req Request, cfg *Config) (*Response, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, errors.New("missing query")
	}
	cfg = cfg.WithDefaults()
	req = normalizeRequest(req)

	registry := NewRegistry()
	registerProviders(registry, cfg)
	order := buildOrder(cfg)

	var lastErr error
	for _, name := range order {
		provider, ok := registry.Get(name)
		if !ok {
			continue
		}
		resp, err := provider.Search(ctx, req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp == nil {
			lastErr = fmt.Errorf("provider %s returned empty response", name)
			continue
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
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("no search providers available")
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

func buildOrder(cfg *Config) []string {
	return stringutil.BuildProviderOrder(cfg.Provider, cfg.Fallbacks, DefaultFallbackOrder)
}

func registerProviders(registry *Registry, cfg *Config) {
	if registry == nil || cfg == nil {
		return
	}

	if p := newProviderIfEnabled(cfg.Exa.Enabled, cfg.Exa.APIKey, func() Provider { return &exaProvider{cfg: cfg.Exa} }); p != nil {
		registry.Register(p)
	}
}

// newProviderIfEnabled returns a Provider when the feature flag is on and the
// API key is non-empty. It returns nil otherwise, centralising the common
// validation that every provider constructor previously duplicated.
func newProviderIfEnabled(enabled *bool, apiKey string, create func() Provider) Provider {
	if !stringutil.BoolPtrOr(enabled, true) {
		return nil
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil
	}
	return create()
}
