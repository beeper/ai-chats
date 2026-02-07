package fetch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Fetch executes a fetch using the configured provider chain.
func Fetch(ctx context.Context, req Request, cfg *Config) (*Response, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, errors.New("missing url")
	}
	cfg = cfg.WithDefaults()
	req = normalizeRequest(req, cfg)

	registry := NewRegistry()
	registerProviders(registry, cfg)
	order := buildOrder(cfg)

	var lastErr error
	for _, name := range order {
		provider := registry.Get(name)
		if provider == nil {
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

func normalizeRequest(req Request, _ *Config) Request {
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
	order := make([]string, 0, len(cfg.Fallbacks)+1)
	provider := strings.TrimSpace(cfg.Provider)
	if provider != "" && provider != "auto" {
		order = append(order, provider)
	}
	order = append(order, cfg.Fallbacks...)
	return dedupeOrder(order)
}

func dedupeOrder(items []string) []string {
	seen := make(map[string]bool, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	if len(result) == 0 {
		return slices.Clone(DefaultFallbackOrder)
	}
	return result
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
