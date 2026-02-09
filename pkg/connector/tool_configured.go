package connector

import (
	"context"
	"strings"

	"github.com/beeper/ai-bridge/pkg/fetch"
	"github.com/beeper/ai-bridge/pkg/search"
)

// These helpers answer "is this tool actually usable/configured right now?"
// Tool policy ("allow/deny") is handled elsewhere; these checks are about runtime
// prerequisites like API keys and service initialization.

func (oc *AIClient) effectiveSearchConfig(ctx context.Context) *search.Config {
	if ctx == nil {
		ctx = context.Background()
	}
	var cfg *search.Config
	var meta *UserLoginMetadata
	var connector *OpenAIConnector
	if oc != nil {
		connector = oc.connector
		if connector != nil {
			cfg = mapSearchConfig(connector.Config.Tools.Search)
		}
		if oc.UserLogin != nil {
			meta = loginMetadata(oc.UserLogin)
		}
	}
	cfg = applyLoginTokensToSearchConfig(cfg, meta, connector)
	return search.ApplyEnvDefaults(cfg).WithDefaults()
}

func (oc *AIClient) effectiveFetchConfig(ctx context.Context) *fetch.Config {
	if ctx == nil {
		ctx = context.Background()
	}
	var cfg *fetch.Config
	var meta *UserLoginMetadata
	var connector *OpenAIConnector
	if oc != nil {
		connector = oc.connector
		if connector != nil {
			cfg = mapFetchConfig(connector.Config.Tools.Fetch)
		}
		if oc.UserLogin != nil {
			meta = loginMetadata(oc.UserLogin)
		}
	}
	cfg = applyLoginTokensToFetchConfig(cfg, meta, connector)
	return fetch.ApplyEnvDefaults(cfg).WithDefaults()
}

func (oc *AIClient) isWebSearchConfigured(ctx context.Context) (bool, string) {
	cfg := oc.effectiveSearchConfig(ctx)
	// Mirrors pkg/search/router.go provider registration requirements.
	if strings.TrimSpace(cfg.Exa.APIKey) != "" {
		if searchEnabled(cfg.Exa.Enabled) {
			return true, ""
		}
	}
	if strings.TrimSpace(cfg.Brave.APIKey) != "" {
		if searchEnabled(cfg.Brave.Enabled) {
			return true, ""
		}
	}
	if strings.TrimSpace(cfg.Perplexity.APIKey) != "" {
		if searchEnabled(cfg.Perplexity.Enabled) {
			return true, ""
		}
	}
	if strings.TrimSpace(cfg.OpenRouter.APIKey) != "" {
		if searchEnabled(cfg.OpenRouter.Enabled) {
			return true, ""
		}
	}
	return false, "Web search is not configured (missing API key for all providers)"
}

func (oc *AIClient) isWebFetchConfigured(ctx context.Context) (bool, string) {
	cfg := oc.effectiveFetchConfig(ctx)
	// Exa requires an API key; direct does not.
	if strings.TrimSpace(cfg.Exa.APIKey) != "" && fetchEnabled(cfg.Exa.Enabled) {
		return true, ""
	}
	if fetchEnabled(cfg.Direct.Enabled) {
		return true, ""
	}
	return false, "Web fetch is disabled (direct disabled and Exa API key missing)"
}

func (oc *AIClient) isMemorySearchExplicitlyDisabled(meta *PortalMetadata) (bool, string) {
	if oc == nil || oc.connector == nil {
		return true, "Missing connector"
	}
	agentID := resolveAgentID(meta)
	cfg, err := resolveMemorySearchConfig(oc, agentID)
	if err != nil {
		// resolveMemorySearchConfig returns an error when connector is missing or when the
		// tool is disabled. Treat both as unavailable here.
		return true, err.Error()
	}
	if cfg == nil || !cfg.Enabled {
		return true, "Memory search disabled"
	}
	return false, ""
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

func (oc *AIClient) isCronConfigured() (bool, string) {
	if oc == nil || oc.cronService == nil {
		return false, "Cron service not available"
	}
	return true, ""
}

func searchEnabled(flag *bool) bool {
	if flag == nil {
		return true
	}
	return *flag
}

func fetchEnabled(flag *bool) bool {
	if flag == nil {
		return true
	}
	return *flag
}
