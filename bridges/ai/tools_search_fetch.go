package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/beeper/agentremote/pkg/fetch"
	"github.com/beeper/agentremote/pkg/search"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/pkg/shared/websearch"
)

func executeWebSearchWithProviders(ctx context.Context, args map[string]any) (string, error) {
	req, err := websearch.RequestFromArgs(args)
	if err != nil {
		return "", err
	}

	btc := GetBridgeToolContext(ctx)
	var cfg *search.Config
	if btc != nil && btc.Client != nil {
		cfg = btc.Client.effectiveSearchConfig(ctx)
	}
	resp, err := search.Search(ctx, req, cfg)
	if err != nil {
		return "", err
	}

	payload := websearch.PayloadFromResponse(resp)
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to encode web_search response: %w", err)
	}
	return string(raw), nil
}

func executeWebFetchWithProviders(ctx context.Context, args map[string]any) (string, error) {
	urlStr, ok := args["url"].(string)
	if !ok {
		return "", errors.New("missing or invalid 'url' argument")
	}
	urlStr = strings.TrimSpace(urlStr)
	if urlStr == "" {
		return "", errors.New("missing or invalid 'url' argument")
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

	btc := GetBridgeToolContext(ctx)
	var cfg *fetch.Config
	if btc != nil && btc.Client != nil {
		cfg = btc.Client.effectiveFetchConfig(ctx)
	}
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

func applyLoginTokensToSearchConfig(cfg *search.Config, meta *UserLoginMetadata, connector *OpenAIConnector) *search.Config {
	if cfg == nil {
		cfg = &search.Config{}
	}
	if meta == nil || connector == nil {
		return cfg
	}

	applyResolvedExaConfig(&cfg.Exa.BaseURL, &cfg.Exa.APIKey, meta, connector)
	if shouldApplyExaProxyDefaults(meta) {
		applyExaProxyDefaults(cfg, meta, connector)
	}
	if shouldForceExaProvider(cfg.Exa.APIKey, cfg.Exa.BaseURL, meta) {
		applyProviderOverride(&cfg.Provider, &cfg.Fallbacks, search.ProviderExa)
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

	applyResolvedExaConfig(&cfg.Exa.BaseURL, &cfg.Exa.APIKey, meta, connector)
	if shouldApplyExaProxyDefaults(meta) {
		applyFetchExaProxyDefaults(cfg, meta, connector)
	}
	if shouldForceExaProvider(cfg.Exa.APIKey, cfg.Exa.BaseURL, meta) {
		applyProviderOverride(&cfg.Provider, &cfg.Fallbacks, fetch.ProviderExa)
	}

	return cfg
}

func applyResolvedExaConfig(baseURL *string, apiKey *string, meta *UserLoginMetadata, connector *OpenAIConnector) {
	if meta == nil || connector == nil {
		return
	}
	services := connector.resolveServiceConfig(meta)
	if apiKey != nil && *apiKey == "" {
		*apiKey = services[serviceExa].APIKey
	}
	if baseURL != nil && *baseURL == "" {
		*baseURL = services[serviceExa].BaseURL
	}
}

func shouldApplyExaProxyDefaults(meta *UserLoginMetadata) bool {
	if meta == nil {
		return false
	}
	return meta.Provider == ProviderMagicProxy
}

func shouldForceExaProvider(apiKey, baseURL string, meta *UserLoginMetadata) bool {
	if isMagicProxyLogin(meta) {
		return true
	}
	return hasExaTokenAndCustomEndpoint(apiKey, baseURL)
}

func isMagicProxyLogin(meta *UserLoginMetadata) bool {
	return meta != nil && meta.Provider == ProviderMagicProxy
}

func hasExaTokenAndCustomEndpoint(apiKey, baseURL string) bool {
	if strings.TrimSpace(apiKey) == "" {
		return false
	}
	return isCustomExaEndpoint(baseURL)
}

func isCustomExaEndpoint(baseURL string) bool {
	trimmed := stringutil.NormalizeBaseURL(baseURL)
	if trimmed == "" {
		return false
	}
	return !strings.EqualFold(trimmed, "https://api.exa.ai")
}

func applyProviderOverride(provider *string, fallbacks *[]string, providerName string) {
	if provider != nil {
		*provider = providerName
	}
	if fallbacks != nil {
		*fallbacks = []string{providerName}
	}
}

func applyExaProxyDefaultsTo(baseURL *string, apiKey *string, meta *UserLoginMetadata, connector *OpenAIConnector) {
	if connector == nil {
		return
	}
	proxyRoot := connector.resolveProxyRoot(meta)
	if proxyRoot == "" {
		return
	}
	if isRelativePath(*baseURL) {
		*baseURL = joinProxyPath(proxyRoot, *baseURL)
	} else if shouldUseExaProxyBase(*baseURL) {
		if proxyBase := connector.resolveExaProxyBaseURL(meta); proxyBase != "" {
			*baseURL = proxyBase
		}
	}
	if *apiKey == "" {
		if meta != nil && meta.Provider == ProviderMagicProxy {
			if token := strings.TrimSpace(meta.APIKey); token != "" {
				*apiKey = token
			}
		}
	}
}

func applyExaProxyDefaults(cfg *search.Config, meta *UserLoginMetadata, connector *OpenAIConnector) {
	if cfg == nil {
		return
	}
	applyExaProxyDefaultsTo(&cfg.Exa.BaseURL, &cfg.Exa.APIKey, meta, connector)
}

func applyFetchExaProxyDefaults(cfg *fetch.Config, meta *UserLoginMetadata, connector *OpenAIConnector) {
	if cfg == nil {
		return
	}
	applyExaProxyDefaultsTo(&cfg.Exa.BaseURL, &cfg.Exa.APIKey, meta, connector)
}

func shouldUseExaProxyBase(baseURL string) bool {
	trimmed := stringutil.NormalizeBaseURL(baseURL)
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
