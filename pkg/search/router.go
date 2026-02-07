package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
		provider := registry.Get(name)
		if provider == nil {
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
	if p := newBraveProvider(cfg); p != nil {
		registry.Register(p)
	}
	if p := newPerplexityProvider(cfg); p != nil {
		registry.Register(p)
	}
	if p := newOpenRouterProvider(cfg); p != nil {
		registry.Register(p)
	}
}
