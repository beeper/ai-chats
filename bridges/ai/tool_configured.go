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

func (oc *AIClient) retrievalConfigContext(ctx context.Context) (string, *aiLoginConfig, *OpenAIConnector) {
	var provider string
	var loginCfg *aiLoginConfig
	var connector *OpenAIConnector
	if oc != nil {
		connector = oc.connector
		if oc.UserLogin != nil {
			provider = loginMetadata(oc.UserLogin).Provider
			loginCfg = oc.loginConfigSnapshot(ctx)
		}
	}
	return provider, loginCfg, connector
}

func applyRetrievalConfigRuntimeDefaults(providerField *string, fallbacks *[]string, exaBaseURL *string, exaAPIKey *string, envProviderKey, envFallbacksKey, provider string, loginCfg *aiLoginConfig, connector *OpenAIConnector) {
	applyLoginTokensToRetrievalConfig(providerField, fallbacks, exaBaseURL, exaAPIKey, provider, loginCfg, connector)
	if providerField != nil && *providerField == "" {
		*providerField = strings.TrimSpace(os.Getenv(envProviderKey))
	}
	if fallbacks != nil && len(*fallbacks) == 0 {
		if raw := strings.TrimSpace(os.Getenv(envFallbacksKey)); raw != "" {
			*fallbacks = stringutil.SplitCSV(raw)
		}
	}
	exa.ApplyEnv(exaAPIKey, exaBaseURL)
}

func (oc *AIClient) effectiveSearchConfig(ctx context.Context) *retrieval.SearchConfig {
	var cfg *retrieval.SearchConfig
	provider, loginCfg, connector := oc.retrievalConfigContext(ctx)
	if connector != nil && connector.Config.Tools.Web != nil {
		cfg = mapSearchConfig(connector.Config.Tools.Web.Search)
	}
	if cfg == nil {
		cfg = &retrieval.SearchConfig{}
	}
	applyRetrievalConfigRuntimeDefaults(&cfg.Provider, &cfg.Fallbacks, &cfg.Exa.BaseURL, &cfg.Exa.APIKey, "SEARCH_PROVIDER", "SEARCH_FALLBACKS", provider, loginCfg, connector)
	return cfg.WithDefaults()
}

func (oc *AIClient) effectiveFetchConfig(ctx context.Context) *retrieval.FetchConfig {
	var cfg *retrieval.FetchConfig
	provider, loginCfg, connector := oc.retrievalConfigContext(ctx)
	if connector != nil && connector.Config.Tools.Web != nil {
		cfg = mapFetchConfig(connector.Config.Tools.Web.Fetch)
	}
	if cfg == nil {
		cfg = &retrieval.FetchConfig{}
	}
	applyRetrievalConfigRuntimeDefaults(&cfg.Provider, &cfg.Fallbacks, &cfg.Exa.BaseURL, &cfg.Exa.APIKey, "FETCH_PROVIDER", "FETCH_FALLBACKS", provider, loginCfg, connector)
	return cfg.WithDefaults()
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
