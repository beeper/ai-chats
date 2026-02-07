package fetch

import (
	"os"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

// ConfigFromEnv builds a fetch config using environment variables.
func ConfigFromEnv() *Config {
	cfg := (&Config{}).WithDefaults()

	if provider := strings.TrimSpace(os.Getenv("FETCH_PROVIDER")); provider != "" {
		cfg.Provider = provider
	}
	if fallbacks := strings.TrimSpace(os.Getenv("FETCH_FALLBACKS")); fallbacks != "" {
		cfg.Fallbacks = stringutil.SplitCSV(fallbacks)
	}

	cfg.Exa.APIKey = envOr(cfg.Exa.APIKey, os.Getenv("EXA_API_KEY"))
	cfg.Exa.BaseURL = envOr(cfg.Exa.BaseURL, os.Getenv("EXA_BASE_URL"))

	return cfg
}

// ApplyEnvDefaults fills empty config fields from environment variables.
func ApplyEnvDefaults(cfg *Config) *Config {
	if cfg == nil {
		return ConfigFromEnv()
	}
	current := cfg.WithDefaults()
	envCfg := ConfigFromEnv()

	if strings.TrimSpace(current.Provider) == "" {
		current.Provider = envCfg.Provider
	}
	if len(current.Fallbacks) == 0 {
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

func envOr(existing, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return existing
	}
	return value
}
