package fetch

import (
	"os"

	"github.com/beeper/agentremote/pkg/shared/exa"
	"github.com/beeper/agentremote/pkg/shared/providerkit"
	"github.com/beeper/agentremote/pkg/shared/providerresource"
)

// ConfigFromEnv builds a fetch config using environment variables.
func ConfigFromEnv() *Config {
	cfg := &Config{}
	providerkit.ApplyNamedEnv(&cfg.Provider, &cfg.Fallbacks, os.Getenv("FETCH_PROVIDER"), os.Getenv("FETCH_FALLBACKS"))
	exa.ApplyEnv(&cfg.Exa.APIKey, &cfg.Exa.BaseURL)
	return cfg.WithDefaults()
}

// ApplyEnvDefaults fills empty config fields from environment variables.
func ApplyEnvDefaults(cfg *Config) *Config {
	return providerresource.ApplyEnvDefaults(
		cfg,
		ConfigFromEnv,
		func(current *Config) *Config { return current.WithDefaults() },
		func(current *Config) bool { return current != nil && current.Provider != "" },
		func(current *Config) bool { return current != nil && len(current.Fallbacks) > 0 },
		func(current, env *Config, hasProvider, hasFallbacks bool) {
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
