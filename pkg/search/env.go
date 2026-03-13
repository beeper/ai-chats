package search

import (
	"os"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/exa"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

// ConfigFromEnv builds a search config using environment variables.
func ConfigFromEnv() *Config {
	cfg := &Config{}

	if provider := strings.TrimSpace(os.Getenv("SEARCH_PROVIDER")); provider != "" {
		cfg.Provider = provider
	}
	if fallbacks := strings.TrimSpace(os.Getenv("SEARCH_FALLBACKS")); fallbacks != "" {
		cfg.Fallbacks = stringutil.SplitCSV(fallbacks)
	}
	exa.ApplyEnv(&cfg.Exa.APIKey, &cfg.Exa.BaseURL)

	return cfg.WithDefaults()
}

// ApplyEnvDefaults fills empty config fields from environment variables.
func ApplyEnvDefaults(cfg *Config) *Config {
	if cfg == nil {
		return ConfigFromEnv()
	}
	current := cfg.WithDefaults()
	envCfg := ConfigFromEnv()

	// WithDefaults already fills Provider and Fallbacks, so only credentials
	// need merging from the environment.
	if current.Exa.APIKey == "" {
		current.Exa.APIKey = envCfg.Exa.APIKey
	}
	if current.Exa.BaseURL == "" {
		current.Exa.BaseURL = envCfg.Exa.BaseURL
	}

	return current
}
