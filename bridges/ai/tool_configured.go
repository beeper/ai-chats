package ai

import (
	"context"
	"os"
	"strings"

	"github.com/beeper/agentremote/pkg/retrieval"
	"github.com/beeper/agentremote/pkg/shared/exa"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

// These helpers answer "is this tool actually usable/configured right now?"
// Tool policy ("allow/deny") is handled elsewhere; these checks are about runtime
// prerequisites like API keys and service initialization.

func (oc *AIClient) effectiveSearchConfig(ctx context.Context) *retrieval.SearchConfig {
	return effectiveToolConfig(
		ctx,
		oc,
		func(connector *OpenAIConnector) *retrieval.SearchConfig {
			if connector == nil || connector.Config.Tools.Web == nil {
				return nil
			}
			return mapSearchConfig(connector.Config.Tools.Web.Search)
		},
		func(cfg *retrieval.SearchConfig, provider string, loginCfg *aiLoginConfig, connector *OpenAIConnector) *retrieval.SearchConfig {
			if cfg == nil {
				cfg = &retrieval.SearchConfig{}
			}
			applyLoginTokensToRetrievalConfig(&cfg.Provider, &cfg.Fallbacks, &cfg.Exa.BaseURL, &cfg.Exa.APIKey, provider, loginCfg, connector)
			return cfg
		},
		func(cfg *retrieval.SearchConfig) *retrieval.SearchConfig {
			envCfg := &retrieval.SearchConfig{}
			envCfg.Provider = stringutil.EnvOr(envCfg.Provider, os.Getenv("SEARCH_PROVIDER"))
			if len(envCfg.Fallbacks) == 0 {
				if raw := strings.TrimSpace(os.Getenv("SEARCH_FALLBACKS")); raw != "" {
					envCfg.Fallbacks = stringutil.SplitCSV(raw)
				}
			}
			exa.ApplyEnv(&envCfg.Exa.APIKey, &envCfg.Exa.BaseURL)
			envCfg = envCfg.WithDefaults()
			if cfg == nil {
				return envCfg
			}
			hasProvider := cfg.Provider != ""
			hasFallbacks := len(cfg.Fallbacks) > 0
			current := cfg.WithDefaults()
			if !hasProvider {
				current.Provider = envCfg.Provider
			}
			if !hasFallbacks {
				current.Fallbacks = envCfg.Fallbacks
			}
			if current.Exa.APIKey == "" {
				current.Exa.APIKey = envCfg.Exa.APIKey
			}
			if current.Exa.BaseURL == "" {
				current.Exa.BaseURL = envCfg.Exa.BaseURL
			}
			return current
		},
	)
}

func (oc *AIClient) effectiveFetchConfig(ctx context.Context) *retrieval.FetchConfig {
	return effectiveToolConfig(
		ctx,
		oc,
		func(connector *OpenAIConnector) *retrieval.FetchConfig {
			if connector == nil || connector.Config.Tools.Web == nil {
				return nil
			}
			return mapFetchConfig(connector.Config.Tools.Web.Fetch)
		},
		func(cfg *retrieval.FetchConfig, provider string, loginCfg *aiLoginConfig, connector *OpenAIConnector) *retrieval.FetchConfig {
			if cfg == nil {
				cfg = &retrieval.FetchConfig{}
			}
			applyLoginTokensToRetrievalConfig(&cfg.Provider, &cfg.Fallbacks, &cfg.Exa.BaseURL, &cfg.Exa.APIKey, provider, loginCfg, connector)
			return cfg
		},
		func(cfg *retrieval.FetchConfig) *retrieval.FetchConfig {
			envCfg := &retrieval.FetchConfig{}
			envCfg.Provider = stringutil.EnvOr(envCfg.Provider, os.Getenv("FETCH_PROVIDER"))
			if len(envCfg.Fallbacks) == 0 {
				if raw := strings.TrimSpace(os.Getenv("FETCH_FALLBACKS")); raw != "" {
					envCfg.Fallbacks = stringutil.SplitCSV(raw)
				}
			}
			exa.ApplyEnv(&envCfg.Exa.APIKey, &envCfg.Exa.BaseURL)
			envCfg = envCfg.WithDefaults()
			if cfg == nil {
				return envCfg
			}
			hasProvider := cfg.Provider != ""
			hasFallbacks := len(cfg.Fallbacks) > 0
			current := cfg.WithDefaults()
			if !hasProvider {
				current.Provider = envCfg.Provider
			}
			if !hasFallbacks {
				current.Fallbacks = envCfg.Fallbacks
			}
			if current.Exa.APIKey == "" {
				current.Exa.APIKey = envCfg.Exa.APIKey
			}
			if current.Exa.BaseURL == "" {
				current.Exa.BaseURL = envCfg.Exa.BaseURL
			}
			return current
		},
	)
}

func effectiveToolConfig[T any](
	ctx context.Context,
	oc *AIClient,
	load func(*OpenAIConnector) *T,
	applyTokens func(*T, string, *aiLoginConfig, *OpenAIConnector) *T,
	withDefaults func(*T) *T,
) *T {
	var cfg *T
	var provider string
	var loginCfg *aiLoginConfig
	var connector *OpenAIConnector
	if oc != nil {
		connector = oc.connector
		cfg = load(connector)
		if oc.UserLogin != nil {
			provider = loginMetadata(oc.UserLogin).Provider
			loginCfg = oc.loginConfigSnapshot(ctx)
		}
	}
	cfg = applyTokens(cfg, provider, loginCfg, connector)
	return withDefaults(cfg)
}

func (oc *AIClient) isWebSearchConfigured(ctx context.Context) (bool, string) {
	cfg := oc.effectiveSearchConfig(ctx)
	// Mirrors pkg/retrieval/search.go provider registration requirements.
	if strings.TrimSpace(cfg.Exa.APIKey) != "" {
		if stringutil.BoolPtrOr(cfg.Exa.Enabled, true) {
			return true, ""
		}
	}
	return false, "Web search is not configured (missing Exa API key)"
}

func (oc *AIClient) isWebFetchConfigured(ctx context.Context) (bool, string) {
	cfg := oc.effectiveFetchConfig(ctx)
	// Exa requires an API key; direct does not.
	if strings.TrimSpace(cfg.Exa.APIKey) != "" && stringutil.BoolPtrOr(cfg.Exa.Enabled, true) {
		return true, ""
	}
	if stringutil.BoolPtrOr(cfg.Direct.Enabled, true) {
		return true, ""
	}
	return false, "Web fetch is disabled (direct disabled and Exa API key missing)"
}

func (oc *AIClient) isTTSConfigured() (bool, string) {
	// macOS fallback is always available (uses the system "say" command).
	if isTTSMacOSAvailable() {
		return true, ""
	}
	// Provider-based TTS requires a provider that supports /v1/audio/speech plus an API key.
	if oc == nil || oc.provider == nil {
		return false, "TTS not available"
	}
	// apiKey is the credential used by callOpenAITTS.
	if strings.TrimSpace(oc.apiKey) == "" {
		return false, "TTS not configured: missing API key"
	}
	// Use the same base URL capability heuristic as execution.
	btc := &BridgeToolContext{Client: oc}
	_, supports := resolveOpenAITTSBaseURL(btc, oc.provider.baseURL)
	if !supports {
		return false, "TTS not available for this provider"
	}
	return true, ""
}
