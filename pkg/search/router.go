package search

import (
	"context"
	"errors"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/providerresource"
	"github.com/beeper/agentremote/pkg/shared/registry"
)

// Search executes a search using the configured provider chain.
func Search(ctx context.Context, req Request, cfg *Config) (*Response, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, errors.New("missing query")
	}
	cfg = cfg.WithDefaults()
	req = normalizeRequest(req)

	return providerresource.Run(
		cfg.Provider,
		cfg.Fallbacks,
		DefaultFallbackOrder,
		func(reg *registry.Registry[Provider]) {
			registerProviders(reg, cfg)
		},
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

func registerProviders(reg *registry.Registry[Provider], cfg *Config) {
	if p := newExaProvider(cfg); p != nil {
		reg.Register(p)
	}
}
