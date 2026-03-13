package search

import (
	"context"
	"errors"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/exa"
	"github.com/beeper/agentremote/pkg/shared/providerchain"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
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

	return providerchain.RunFirst(
		order,
		registry.Get,
		func(provider Provider) (*Response, error) {
			return provider.Search(ctx, req)
		},
		func(name string, resp *Response) {
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
	if exa.Enabled(cfg.Exa.Enabled, cfg.Exa.APIKey) {
		p := &exaProvider{cfg: cfg.Exa}
		registry.Register(p)
	}
}
