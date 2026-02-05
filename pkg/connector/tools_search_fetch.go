package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/beeper/ai-bridge/pkg/fetch"
	"github.com/beeper/ai-bridge/pkg/search"
	"github.com/beeper/ai-bridge/pkg/shared/websearch"
)

func executeWebSearchWithProviders(ctx context.Context, args map[string]any) (string, error) {
	req, err := searchRequestFromArgs(args)
	if err != nil {
		return "", err
	}

	cfg := resolveSearchConfig(ctx)
	resp, err := search.Search(ctx, req, cfg)
	if err != nil {
		return "", err
	}

	payload := buildSearchPayload(resp)
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to encode web_search response: %w", err)
	}
	return string(raw), nil
}

func executeWebFetchWithProviders(ctx context.Context, args map[string]any) (string, error) {
	urlStr, ok := args["url"].(string)
	if !ok {
		return "", fmt.Errorf("missing or invalid 'url' argument")
	}
	urlStr = strings.TrimSpace(urlStr)
	if urlStr == "" {
		return "", fmt.Errorf("missing or invalid 'url' argument")
	}

	extractMode := "markdown"
	if mode, ok := args["extractMode"].(string); ok && strings.EqualFold(strings.TrimSpace(mode), "text") {
		extractMode = "text"
	}

	maxChars := 0
	if mc, ok := args["maxChars"].(float64); ok && mc > 0 {
		maxChars = int(mc)
	}

	req := fetch.Request{
		URL:         urlStr,
		ExtractMode: extractMode,
		MaxChars:    maxChars,
	}

	cfg := resolveFetchConfig(ctx)
	resp, err := fetch.Fetch(ctx, req, cfg)
	if err != nil {
		return "", err
	}

	payload := map[string]any{
		"url":           resp.URL,
		"finalUrl":      resp.FinalURL,
		"status":        resp.Status,
		"contentType":   resp.ContentType,
		"extractMode":   resp.ExtractMode,
		"extractor":     resp.Extractor,
		"truncated":     resp.Truncated,
		"length":        resp.Length,
		"rawLength":     resp.RawLength,
		"wrappedLength": resp.WrappedLength,
		"fetchedAt":     resp.FetchedAt,
		"tookMs":        resp.TookMs,
		"text":          resp.Text,
		"content":       resp.Text,
		"provider":      resp.Provider,
		"warning":       resp.Warning,
		"cached":        resp.Cached,
	}
	if resp.Extras != nil {
		payload["extras"] = resp.Extras
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to encode web_fetch response: %w", err)
	}
	return string(raw), nil
}

func searchRequestFromArgs(args map[string]any) (search.Request, error) {
	query, ok := args["query"].(string)
	if !ok {
		return search.Request{}, fmt.Errorf("missing or invalid 'query' argument")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return search.Request{}, fmt.Errorf("missing or invalid 'query' argument")
	}
	count, _ := websearch.ParseCountAndIgnoredOptions(args)
	country, _ := args["country"].(string)
	searchLang, _ := args["search_lang"].(string)
	uiLang, _ := args["ui_lang"].(string)
	freshness, _ := args["freshness"].(string)

	return search.Request{
		Query:      query,
		Count:      count,
		Country:    strings.TrimSpace(country),
		SearchLang: strings.TrimSpace(searchLang),
		UILang:     strings.TrimSpace(uiLang),
		Freshness:  strings.TrimSpace(freshness),
	}, nil
}

func buildSearchPayload(resp *search.Response) map[string]any {
	payload := map[string]any{
		"query":      resp.Query,
		"provider":   resp.Provider,
		"count":      resp.Count,
		"tookMs":     resp.TookMs,
		"answer":     resp.Answer,
		"summary":    resp.Summary,
		"definition": resp.Definition,
		"warning":    resp.Warning,
		"noResults":  resp.NoResults,
		"cached":     resp.Cached,
	}

	if len(resp.Results) > 0 {
		results := make([]map[string]any, 0, len(resp.Results))
		for _, r := range resp.Results {
			results = append(results, map[string]any{
				"title":       r.Title,
				"url":         r.URL,
				"description": r.Description,
				"published":   r.Published,
				"siteName":    r.SiteName,
			})
		}
		payload["results"] = results
	}

	if resp.Extras != nil {
		payload["extras"] = resp.Extras
	}
	return payload
}

func resolveSearchConfig(ctx context.Context) *search.Config {
	var cfg *search.Config
	var meta *UserLoginMetadata
	var connector *OpenAIConnector
	if btc := GetBridgeToolContext(ctx); btc != nil && btc.Client != nil {
		connector = btc.Client.connector
		src := connector.Config.Tools.Search
		cfg = mapSearchConfig(src)
		if btc.Client.UserLogin != nil {
			meta = loginMetadata(btc.Client.UserLogin)
		}
	}
	cfg = applyLoginTokensToSearchConfig(cfg, meta, connector)
	return search.ApplyEnvDefaults(cfg)
}

func resolveFetchConfig(ctx context.Context) *fetch.Config {
	var cfg *fetch.Config
	var meta *UserLoginMetadata
	var connector *OpenAIConnector
	if btc := GetBridgeToolContext(ctx); btc != nil && btc.Client != nil {
		connector = btc.Client.connector
		src := connector.Config.Tools.Fetch
		cfg = mapFetchConfig(src)
		if btc.Client.UserLogin != nil {
			meta = loginMetadata(btc.Client.UserLogin)
		}
	}
	cfg = applyLoginTokensToFetchConfig(cfg, meta, connector)
	return fetch.ApplyEnvDefaults(cfg)
}

func applyLoginTokensToSearchConfig(cfg *search.Config, meta *UserLoginMetadata, connector *OpenAIConnector) *search.Config {
	if cfg == nil {
		cfg = &search.Config{}
	}
	if meta == nil || connector == nil {
		return cfg
	}

	services := connector.resolveServiceConfig(meta)
	if cfg.Exa.APIKey == "" {
		cfg.Exa.APIKey = services[serviceExa].APIKey
	}
	if cfg.Exa.BaseURL == "" {
		cfg.Exa.BaseURL = services[serviceExa].BaseURL
	}
	if cfg.Brave.APIKey == "" {
		cfg.Brave.APIKey = services[serviceBrave].APIKey
	}
	if cfg.Perplexity.APIKey == "" {
		cfg.Perplexity.APIKey = services[servicePerplexity].APIKey
	}
	if cfg.OpenRouter.APIKey == "" {
		cfg.OpenRouter.APIKey = services[serviceOpenRouter].APIKey
	}
	if cfg.OpenRouter.BaseURL == "" {
		cfg.OpenRouter.BaseURL = services[serviceOpenRouter].BaseURL
	}

	if shouldForceExaSearchProvider(meta) {
		forceSearchProviderExa(cfg)
		applyExaProxyDefaults(cfg, meta, connector)
	}

	return cfg
}

func applyLoginTokensToFetchConfig(cfg *fetch.Config, meta *UserLoginMetadata, connector *OpenAIConnector) *fetch.Config {
	if cfg == nil {
		cfg = &fetch.Config{}
	}
	if meta == nil || connector == nil {
		return cfg
	}

	services := connector.resolveServiceConfig(meta)
	if cfg.Exa.APIKey == "" {
		cfg.Exa.APIKey = services[serviceExa].APIKey
	}
	if cfg.Exa.BaseURL == "" {
		cfg.Exa.BaseURL = services[serviceExa].BaseURL
	}

	if shouldForceExaSearchProvider(meta) {
		applyFetchExaProxyDefaults(cfg, meta, connector)
	}

	return cfg
}

func shouldForceExaSearchProvider(meta *UserLoginMetadata) bool {
	if meta == nil {
		return false
	}
	switch meta.Provider {
	case ProviderBeeper, ProviderOpenAI, ProviderOpenRouter:
		return true
	default:
		return false
	}
}

func forceSearchProviderExa(cfg *search.Config) {
	if cfg == nil {
		return
	}
	cfg.Provider = search.ProviderExa
}

func applyExaProxyDefaults(cfg *search.Config, meta *UserLoginMetadata, connector *OpenAIConnector) {
	if cfg == nil || connector == nil {
		return
	}
	proxyRoot := connector.resolveProxyRoot(meta)
	if proxyRoot == "" {
		return
	}
	if isRelativePath(cfg.Exa.BaseURL) {
		cfg.Exa.BaseURL = joinProxyPath(proxyRoot, cfg.Exa.BaseURL)
	} else if shouldUseExaProxyBase(cfg.Exa.BaseURL) {
		if proxyBase := connector.resolveExaProxyBaseURL(meta); proxyBase != "" {
			cfg.Exa.BaseURL = proxyBase
		}
	}
	if cfg.Exa.APIKey == "" {
		if token := connector.resolveBeeperToken(meta); token != "" {
			cfg.Exa.APIKey = token
		}
	}
}

func applyFetchExaProxyDefaults(cfg *fetch.Config, meta *UserLoginMetadata, connector *OpenAIConnector) {
	if cfg == nil || connector == nil {
		return
	}
	proxyRoot := connector.resolveProxyRoot(meta)
	if proxyRoot == "" {
		return
	}
	if isRelativePath(cfg.Exa.BaseURL) {
		cfg.Exa.BaseURL = joinProxyPath(proxyRoot, cfg.Exa.BaseURL)
	} else if shouldUseExaProxyBase(cfg.Exa.BaseURL) {
		if proxyBase := connector.resolveExaProxyBaseURL(meta); proxyBase != "" {
			cfg.Exa.BaseURL = proxyBase
		}
	}
	if cfg.Exa.APIKey == "" {
		if token := connector.resolveBeeperToken(meta); token != "" {
			cfg.Exa.APIKey = token
		}
	}
}

func shouldUseExaProxyBase(baseURL string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return true
	}
	return strings.EqualFold(trimmed, "https://api.exa.ai")
}

func isRelativePath(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "/")
}

func mapSearchConfig(src *SearchConfig) *search.Config {
	if src == nil {
		return nil
	}
	return &search.Config{
		Provider:  src.Provider,
		Fallbacks: src.Fallbacks,
		Exa: search.ExaConfig{
			Enabled:           src.Exa.Enabled,
			BaseURL:           src.Exa.BaseURL,
			APIKey:            src.Exa.APIKey,
			Type:              src.Exa.Type,
			Category:          src.Exa.Category,
			NumResults:        src.Exa.NumResults,
			IncludeText:       src.Exa.IncludeText,
			TextMaxCharacters: src.Exa.TextMaxCharacters,
			Highlights:        src.Exa.Highlights,
		},
		Brave: search.BraveConfig{
			Enabled:          src.Brave.Enabled,
			BaseURL:          src.Brave.BaseURL,
			APIKey:           src.Brave.APIKey,
			TimeoutSecs:      src.Brave.TimeoutSecs,
			CacheTtlSecs:     src.Brave.CacheTtlSecs,
			SearchLang:       src.Brave.SearchLang,
			UILang:           src.Brave.UILang,
			DefaultCountry:   src.Brave.DefaultCountry,
			DefaultFreshness: src.Brave.DefaultFreshness,
		},
		Perplexity: search.PerplexityConfig{
			Enabled:      src.Perplexity.Enabled,
			APIKey:       src.Perplexity.APIKey,
			BaseURL:      src.Perplexity.BaseURL,
			Model:        src.Perplexity.Model,
			TimeoutSecs:  src.Perplexity.TimeoutSecs,
			CacheTtlSecs: src.Perplexity.CacheTtlSecs,
		},
		OpenRouter: search.OpenRouterConfig{
			Enabled:      src.OpenRouter.Enabled,
			APIKey:       src.OpenRouter.APIKey,
			BaseURL:      src.OpenRouter.BaseURL,
			Model:        src.OpenRouter.Model,
			TimeoutSecs:  src.OpenRouter.TimeoutSecs,
			CacheTtlSecs: src.OpenRouter.CacheTtlSecs,
		},
		DDG: search.DDGConfig{
			Enabled:     src.DDG.Enabled,
			TimeoutSecs: src.DDG.TimeoutSecs,
		},
	}
}

func mapFetchConfig(src *FetchConfig) *fetch.Config {
	if src == nil {
		return nil
	}
	return &fetch.Config{
		Provider:  src.Provider,
		Fallbacks: src.Fallbacks,
		Exa: fetch.ExaConfig{
			Enabled:           src.Exa.Enabled,
			BaseURL:           src.Exa.BaseURL,
			APIKey:            src.Exa.APIKey,
			IncludeText:       src.Exa.IncludeText,
			TextMaxCharacters: src.Exa.TextMaxCharacters,
		},
		Direct: fetch.DirectConfig{
			Enabled:      src.Direct.Enabled,
			TimeoutSecs:  src.Direct.TimeoutSecs,
			UserAgent:    src.Direct.UserAgent,
			Readability:  src.Direct.Readability,
			MaxChars:     src.Direct.MaxChars,
			MaxRedirects: src.Direct.MaxRedirects,
			CacheTtlSecs: src.Direct.CacheTtlSecs,
		},
	}
}
