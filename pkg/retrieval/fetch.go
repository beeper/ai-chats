package retrieval

import (
	"context"
	"errors"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/providerresource"
	"github.com/beeper/agentremote/pkg/shared/registry"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

// Fetch executes a fetch using the configured provider chain.
func Fetch(ctx context.Context, req FetchRequest, cfg *FetchConfig) (*FetchResponse, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, errors.New("missing url")
	}
	cfg = cfg.WithDefaults()
	if req.ExtractMode == "" {
		req.ExtractMode = "markdown"
	}
	if req.MaxChars < 0 {
		req.MaxChars = 0
	}

	return providerresource.Run(
		cfg.Provider,
		cfg.Fallbacks,
		DefaultFetchFallbackOrder,
		func(reg *registry.Registry[FetchProvider]) {
			if cfg != nil && stringutil.BoolPtrOr(cfg.Exa.Enabled, true) && strings.TrimSpace(cfg.Exa.APIKey) != "" {
				reg.Register(&exaFetchProvider{cfg: cfg.Exa})
			}
			if cfg != nil && stringutil.BoolPtrOr(cfg.Direct.Enabled, true) {
				reg.Register(&directFetchProvider{cfg: cfg.Direct})
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
