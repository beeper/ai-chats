package connector

import (
	"context"
	"strings"

	"github.com/beeper/ai-bridge/pkg/fetch"
	"github.com/beeper/ai-bridge/pkg/search"
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

// These helpers answer "is this tool actually usable/configured right now?"
// Tool policy ("allow/deny") is handled elsewhere; these checks are about runtime
// prerequisites like API keys and service initialization.

func (oc *AIClient) effectiveSearchConfig(_ context.Context) *search.Config {
	return effectiveToolConfig(
		oc,
		func(connector *OpenAIConnector) *search.Config {
			if connector == nil {
				return nil
			}
			return mapSearchConfig(connector.Config.Tools.Search)
		},
		applyLoginTokensToSearchConfig,
		func(cfg *search.Config) *search.Config { return search.ApplyEnvDefaults(cfg).WithDefaults() },
	)
}

func (oc *AIClient) effectiveFetchConfig(_ context.Context) *fetch.Config {
	return effectiveToolConfig(
		oc,
		func(connector *OpenAIConnector) *fetch.Config {
			if connector == nil {
				return nil
			}
			return mapFetchConfig(connector.Config.Tools.Fetch)
		},
		applyLoginTokensToFetchConfig,
		func(cfg *fetch.Config) *fetch.Config { return fetch.ApplyEnvDefaults(cfg).WithDefaults() },
	)
}

func effectiveToolConfig[T any](
	oc *AIClient,
	load func(*OpenAIConnector) *T,
	applyTokens func(*T, *UserLoginMetadata, *OpenAIConnector) *T,
	withDefaults func(*T) *T,
) *T {
	var cfg *T
	var meta *UserLoginMetadata
	var connector *OpenAIConnector
	if oc != nil {
		connector = oc.connector
		cfg = load(connector)
		if oc.UserLogin != nil {
			meta = loginMetadata(oc.UserLogin)
		}
	}
	cfg = applyTokens(cfg, meta, connector)
	return withDefaults(cfg)
}

func (oc *AIClient) isWebSearchConfigured(ctx context.Context) (bool, string) {
	cfg := oc.effectiveSearchConfig(ctx)
	// Mirrors pkg/search/router.go provider registration requirements.
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
