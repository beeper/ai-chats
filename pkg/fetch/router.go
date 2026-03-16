package fetch

import (
	"context"
	"errors"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/providerresource"
	"github.com/beeper/agentremote/pkg/shared/registry"
)

// Fetch executes a fetch using the configured provider chain.
func Fetch(ctx context.Context, req Request, cfg *Config) (*Response, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, errors.New("missing url")
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
			return provider.Fetch(ctx, req)
		},
		func(name string, resp *Response) {
			if resp.Provider == "" {
				resp.Provider = name
			}
		},
		errors.New("no fetch providers available"),
	)
}

func normalizeRequest(req Request) Request {
	if req.ExtractMode == "" {
		req.ExtractMode = "markdown"
	}
	if req.MaxChars < 0 {
		req.MaxChars = 0
	}
	return req
}

func registerProviders(reg *registry.Registry[Provider], cfg *Config) {
	if p := newExaProvider(cfg); p != nil {
		reg.Register(p)
	}
	if p := newDirectProvider(cfg); p != nil {
		reg.Register(p)
	}
}
