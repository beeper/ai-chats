package fetch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

// Fetch executes a fetch using the configured provider chain.
func Fetch(ctx context.Context, req Request, cfg *Config) (*Response, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, errors.New("missing url")
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
		resp, err := provider.Fetch(ctx, req)
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
		return resp, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("no fetch providers available")
}

func normalizeRequest(req Request) Request {
	if req.ExtractMode == "" {
		req.ExtractMode = "markdown"
	}
	// Let providers apply their own defaults when max chars is not specified.
	if req.MaxChars < 0 {
		req.MaxChars = 0
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
	if p := newExaProvider(cfg); p != nil {
		registry.Register(p)
	}
	if p := newDirectProvider(cfg); p != nil {
		registry.Register(p)
	}
}
