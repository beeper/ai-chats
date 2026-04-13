package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/beeper/agentremote/pkg/retrieval"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/pkg/shared/websearch"
)

func executeWebSearchWithProviders(ctx context.Context, args map[string]any) (string, error) {
	req, err := websearch.RequestFromArgs(args)
	if err != nil {
		return "", err
	}

	btc := GetBridgeToolContext(ctx)
	var cfg *retrieval.SearchConfig
	if btc != nil && btc.Client != nil {
		cfg = btc.Client.effectiveSearchConfig(ctx)
	}
	resp, err := retrieval.Search(ctx, req, cfg)
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

	req := retrieval.FetchRequest{
		URL:         urlStr,
		ExtractMode: extractMode,
		MaxChars:    maxChars,
	}

	btc := GetBridgeToolContext(ctx)
	var cfg *retrieval.FetchConfig
	if btc != nil && btc.Client != nil {
		cfg = btc.Client.effectiveFetchConfig(ctx)
	}
	resp, err := retrieval.Fetch(ctx, req, cfg)
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

func applyLoginTokensToSearchConfig(cfg *retrieval.SearchConfig, provider string, loginCfg *aiLoginConfig, connector *OpenAIConnector) *retrieval.SearchConfig {
	if cfg == nil {
		cfg = &retrieval.SearchConfig{}
	}
	applyLoginTokensToRetrievalConfig(&cfg.Provider, &cfg.Fallbacks, &cfg.Exa.BaseURL, &cfg.Exa.APIKey, provider, loginCfg, connector)
	return cfg
}

func applyLoginTokensToFetchConfig(cfg *retrieval.FetchConfig, provider string, loginCfg *aiLoginConfig, connector *OpenAIConnector) *retrieval.FetchConfig {
	if cfg == nil {
		cfg = &retrieval.FetchConfig{}
	}
	applyLoginTokensToRetrievalConfig(&cfg.Provider, &cfg.Fallbacks, &cfg.Exa.BaseURL, &cfg.Exa.APIKey, provider, loginCfg, connector)
	return cfg
}

func applyLoginTokensToRetrievalConfig(providerField *string, fallbacks *[]string, exaBaseURL *string, exaAPIKey *string, provider string, loginCfg *aiLoginConfig, connector *OpenAIConnector) {
	if connector == nil {
		return
	}
	applyResolvedExaConfig(exaBaseURL, exaAPIKey, provider, loginCfg, connector)
	if shouldApplyExaProxyDefaults(provider) {
		applyExaProxyDefaultsTo(exaBaseURL, exaAPIKey, provider, loginCfg, connector)
	}
	if shouldForceExaProvider(*exaAPIKey, *exaBaseURL, provider) {
		applyProviderOverride(providerField, fallbacks, retrieval.ProviderExa)
	}
}

func applyResolvedExaConfig(baseURL *string, apiKey *string, provider string, loginCfg *aiLoginConfig, connector *OpenAIConnector) {
	if connector == nil {
		return
	}
	services := connector.resolveServiceConfig(provider, loginCfg)
	if apiKey != nil && *apiKey == "" {
		*apiKey = services[serviceExa].APIKey
	}
	if baseURL != nil && *baseURL == "" {
		*baseURL = services[serviceExa].BaseURL
	}
}

func shouldApplyExaProxyDefaults(provider string) bool {
	return provider == ProviderMagicProxy
}

func shouldForceExaProvider(apiKey, baseURL string, provider string) bool {
	if isMagicProxyLogin(provider) {
		return true
	}
	return hasExaTokenAndCustomEndpoint(apiKey, baseURL)
}

func isMagicProxyLogin(provider string) bool {
	return provider == ProviderMagicProxy
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

func applyExaProxyDefaultsTo(baseURL *string, apiKey *string, provider string, loginCfg *aiLoginConfig, connector *OpenAIConnector) {
	if connector == nil {
		return
	}
	proxyRoot := connector.resolveProxyRoot(loginCfg)
	if proxyRoot == "" {
		return
	}
	if isRelativePath(*baseURL) {
		*baseURL = joinProxyPath(proxyRoot, *baseURL)
	} else if shouldUseExaProxyBase(*baseURL) {
		if proxyBase := connector.resolveExaProxyBaseURL(loginCfg); proxyBase != "" {
			*baseURL = proxyBase
		}
	}
	if *apiKey == "" {
		if provider == ProviderMagicProxy {
			if token := loginCredentialAPIKey(loginCfg); token != "" {
				*apiKey = token
			}
		}
	}
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

func mapSearchConfig(src *SearchConfig) *retrieval.SearchConfig {
	if src == nil {
		return nil
	}
	return &retrieval.SearchConfig{
		Provider:  src.Provider,
		Fallbacks: src.Fallbacks,
		Exa: retrieval.ExaConfig{
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

func mapFetchConfig(src *FetchConfig) *retrieval.FetchConfig {
	if src == nil {
		return nil
	}
	return &retrieval.FetchConfig{
		Provider:  src.Provider,
		Fallbacks: src.Fallbacks,
		Exa: retrieval.ExaConfig{
			Enabled:           src.Exa.Enabled,
			BaseURL:           src.Exa.BaseURL,
			APIKey:            src.Exa.APIKey,
			IncludeText:       src.Exa.IncludeText,
			TextMaxCharacters: src.Exa.TextMaxCharacters,
		},
		Direct: retrieval.DirectConfig{
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
