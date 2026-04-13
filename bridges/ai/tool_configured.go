package ai

import (
	"context"
	"strings"

	"github.com/beeper/agentremote/pkg/retrieval"
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
		applyLoginTokensToSearchConfig,
		func(cfg *retrieval.SearchConfig) *retrieval.SearchConfig {
			return retrieval.SearchApplyEnvDefaults(cfg).WithDefaults()
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
		applyLoginTokensToFetchConfig,
		func(cfg *retrieval.FetchConfig) *retrieval.FetchConfig {
			return retrieval.FetchApplyEnvDefaults(cfg).WithDefaults()
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
	provider, ok := oc.provider.(*OpenAIProvider)
	if !ok {
		return false, "TTS not available: requires OpenAI/Beeper provider or macOS"
	}
	// apiKey is the credential used by callOpenAITTS.
	if strings.TrimSpace(oc.apiKey) == "" {
		return false, "TTS not configured: missing API key"
	}
	// Use the same base URL capability heuristic as execution.
	btc := &BridgeToolContext{Client: oc}
	_, supports := resolveOpenAITTSBaseURL(btc, provider.baseURL)
	if !supports {
		return false, "TTS not available for this provider"
	}
	return true, ""
}
