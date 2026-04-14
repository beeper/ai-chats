package retrieval

import (
	"os"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/exa"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

// SearchApplyEnvDefaults fills empty config fields from environment variables.
func SearchApplyEnvDefaults(cfg *SearchConfig) *SearchConfig {
	envCfg := &SearchConfig{}
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
}

// FetchApplyEnvDefaults fills empty config fields from environment variables.
func FetchApplyEnvDefaults(cfg *FetchConfig) *FetchConfig {
	envCfg := &FetchConfig{}
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
}
