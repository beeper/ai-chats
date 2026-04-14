package retrieval

import (
	"context"
	"errors"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/providerresource"
	"github.com/beeper/agentremote/pkg/shared/registry"
)

// Fetch executes a fetch using the configured provider chain.
func Fetch(ctx context.Context, req FetchRequest, cfg *FetchConfig) (*FetchResponse, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, errors.New("missing url")
	}
	cfg = cfg.WithDefaults()
	req = normalizeFetchRequest(req)

	return providerresource.Run(
		cfg.Provider,
		cfg.Fallbacks,
		DefaultFetchFallbackOrder,
		func(reg *registry.Registry[FetchProvider]) {
			if p := newExaFetchProvider(cfg); p != nil {
				reg.Register(p)
			}
			if p := newDirectFetchProvider(cfg); p != nil {
				reg.Register(p)
			}
		},
		func(provider FetchProvider) (*FetchResponse, error) {
			return provider.Fetch(ctx, req)
		},
		func(name string, resp *FetchResponse) {
			if resp.Provider == "" {
				resp.Provider = name
			}
		},
		errors.New("no fetch providers available"),
	)
}

func normalizeFetchRequest(req FetchRequest) FetchRequest {
	if req.ExtractMode == "" {
		req.ExtractMode = "markdown"
	}
	if req.MaxChars < 0 {
		req.MaxChars = 0
	}
	return req
}
