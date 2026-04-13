package retrieval

import (
	"os"

	"github.com/beeper/agentremote/pkg/shared/exa"
	"github.com/beeper/agentremote/pkg/shared/providerkit"
	"github.com/beeper/agentremote/pkg/shared/providerresource"
)

// SearchConfigFromEnv builds a search config using environment variables.
func SearchConfigFromEnv() *SearchConfig {
	cfg := &SearchConfig{}
	providerkit.ApplyNamedEnv(&cfg.Provider, &cfg.Fallbacks, os.Getenv("SEARCH_PROVIDER"), os.Getenv("SEARCH_FALLBACKS"))
	exa.ApplyEnv(&cfg.Exa.APIKey, &cfg.Exa.BaseURL)
	return cfg.WithDefaults()
}

// FetchConfigFromEnv builds a fetch config using environment variables.
func FetchConfigFromEnv() *FetchConfig {
	cfg := &FetchConfig{}
	providerkit.ApplyNamedEnv(&cfg.Provider, &cfg.Fallbacks, os.Getenv("FETCH_PROVIDER"), os.Getenv("FETCH_FALLBACKS"))
	exa.ApplyEnv(&cfg.Exa.APIKey, &cfg.Exa.BaseURL)
	return cfg.WithDefaults()
}

// SearchApplyEnvDefaults fills empty config fields from environment variables.
func SearchApplyEnvDefaults(cfg *SearchConfig) *SearchConfig {
	return providerresource.ApplyEnvDefaults(
		cfg,
		SearchConfigFromEnv,
		func(current *SearchConfig) *SearchConfig { return current.WithDefaults() },
		func(current *SearchConfig) bool { return current != nil && current.Provider != "" },
		func(current *SearchConfig) bool { return current != nil && len(current.Fallbacks) > 0 },
		func(current, env *SearchConfig, hasProvider, hasFallbacks bool) {
			if !hasProvider {
				current.Provider = env.Provider
			}
			if !hasFallbacks {
				current.Fallbacks = env.Fallbacks
			}
			if current.Exa.APIKey == "" {
				current.Exa.APIKey = env.Exa.APIKey
			}
			if current.Exa.BaseURL == "" {
				current.Exa.BaseURL = env.Exa.BaseURL
			}
		},
	)
}

// FetchApplyEnvDefaults fills empty config fields from environment variables.
func FetchApplyEnvDefaults(cfg *FetchConfig) *FetchConfig {
	return providerresource.ApplyEnvDefaults(
		cfg,
		FetchConfigFromEnv,
		func(current *FetchConfig) *FetchConfig { return current.WithDefaults() },
		func(current *FetchConfig) bool { return current != nil && current.Provider != "" },
		func(current *FetchConfig) bool { return current != nil && len(current.Fallbacks) > 0 },
		func(current, env *FetchConfig, hasProvider, hasFallbacks bool) {
			if !hasProvider {
				current.Provider = env.Provider
			}
			if !hasFallbacks {
				current.Fallbacks = env.Fallbacks
			}
			if current.Exa.APIKey == "" {
				current.Exa.APIKey = env.Exa.APIKey
			}
			if current.Exa.BaseURL == "" {
				current.Exa.BaseURL = env.Exa.BaseURL
			}
		},
	)
}
