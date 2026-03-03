package search

import (
	"os"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
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
	cfg.Exa.APIKey = stringutil.EnvOr(cfg.Exa.APIKey, os.Getenv("EXA_API_KEY"))
	cfg.Exa.BaseURL = stringutil.EnvOr(cfg.Exa.BaseURL, os.Getenv("EXA_BASE_URL"))

	return cfg.WithDefaults()
}

// ApplyEnvDefaults fills empty config fields from environment variables.
func ApplyEnvDefaults(cfg *Config) *Config {
	if cfg == nil {
		return ConfigFromEnv()
	}
	providerExplicit := strings.TrimSpace(cfg.Provider) != ""
	envCfg := ConfigFromEnv()
	if cfg.Exa.APIKey == "" {
		cfg.Exa.APIKey = envCfg.Exa.APIKey
	}
	if cfg.Exa.BaseURL == "" {
		cfg.Exa.BaseURL = envCfg.Exa.BaseURL
	}
	result := cfg.WithDefaults()
	// If no provider was explicitly configured but an API key is available, prefer exa.
	if !providerExplicit && strings.TrimSpace(result.Exa.APIKey) != "" {
		result.Provider = ProviderExa
	}
	return result
}
