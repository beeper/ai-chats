package search

import (
	"os"

	"github.com/beeper/agentremote/pkg/shared/exa"
	"github.com/beeper/agentremote/pkg/shared/providerkit"
)

// ConfigFromEnv builds a search config using environment variables.
func ConfigFromEnv() *Config {
	cfg := &Config{}
	providerkit.ApplyNamedEnv(&cfg.Provider, &cfg.Fallbacks, os.Getenv("SEARCH_PROVIDER"), os.Getenv("SEARCH_FALLBACKS"))
	exa.ApplyEnv(&cfg.Exa.APIKey, &cfg.Exa.BaseURL)

	return cfg.WithDefaults()
}

// ApplyEnvDefaults fills empty config fields from environment variables.
func ApplyEnvDefaults(cfg *Config) *Config {
	if cfg == nil {
		return ConfigFromEnv()
	}
	hasProvider := cfg.Provider != ""
	hasFallbacks := len(cfg.Fallbacks) > 0
	current := cfg.WithDefaults()
	env := ConfigFromEnv()
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
	return current
}
