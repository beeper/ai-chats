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
	if gravatarURL, ok := gravatarProfileURLFromInput(urlStr); ok {
		urlStr = gravatarURL
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

func gravatarProfileURLFromInput(input string) (string, bool) {
	input = strings.TrimSpace(input)
	if input == "" || strings.Contains(input, "://") || !strings.Contains(input, "@") {
		return "", false
	}
	email, err := normalizeGravatarEmail(input)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%s/profiles/%s", gravatarAPIBaseURL, gravatarHash(email)), true
}

func applyLoginTokensToRetrievalConfig(providerField *string, fallbacks *[]string, exaBaseURL *string, exaAPIKey *string, provider string, loginCfg *aiLoginConfig, connector *OpenAIConnector) {
	if connector == nil {
		return
	}
	services := connector.resolveServiceConfig(provider, loginCfg)
	if exaAPIKey != nil && *exaAPIKey == "" {
		*exaAPIKey = services[serviceExa].APIKey
	}
	if exaBaseURL != nil && *exaBaseURL == "" {
		*exaBaseURL = services[serviceExa].BaseURL
	}
	if provider == ProviderMagicProxy {
		proxyRoot := connector.resolveProxyRoot(loginCfg)
		if proxyRoot != "" {
			switch trimmed := strings.TrimSpace(*exaBaseURL); {
			case strings.HasPrefix(trimmed, "/"):
				*exaBaseURL = joinProxyPath(proxyRoot, trimmed)
			default:
				normalized := stringutil.NormalizeBaseURL(*exaBaseURL)
				if normalized == "" || strings.EqualFold(normalized, "https://api.exa.ai") {
					if proxyBase := connector.resolveExaProxyBaseURL(loginCfg); proxyBase != "" {
						*exaBaseURL = proxyBase
					}
				}
			}
		}
		if exaAPIKey != nil && *exaAPIKey == "" {
			if token := loginCredentialAPIKey(loginCfg); token != "" {
				*exaAPIKey = token
			}
		}
	}
	normalizedExaBase := stringutil.NormalizeBaseURL(*exaBaseURL)
	if provider == ProviderMagicProxy || (strings.TrimSpace(*exaAPIKey) != "" &&
		normalizedExaBase != "" &&
		!strings.EqualFold(normalizedExaBase, "https://api.exa.ai")) {
		if providerField != nil {
			*providerField = retrieval.ProviderExa
		}
		if fallbacks != nil {
			*fallbacks = []string{retrieval.ProviderExa}
		}
	}
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
