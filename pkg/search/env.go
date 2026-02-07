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
	cfg.Exa.APIKey = envOr(cfg.Exa.APIKey, os.Getenv("EXA_API_KEY"))
	cfg.Exa.BaseURL = envOr(cfg.Exa.BaseURL, os.Getenv("EXA_BASE_URL"))

	cfg.Brave.APIKey = envOr(cfg.Brave.APIKey, os.Getenv("BRAVE_API_KEY"))
	cfg.Brave.BaseURL = envOr(cfg.Brave.BaseURL, os.Getenv("BRAVE_BASE_URL"))

	cfg.Perplexity.APIKey = envOr(cfg.Perplexity.APIKey, os.Getenv("PERPLEXITY_API_KEY"))
	cfg.Perplexity.BaseURL = envOr(cfg.Perplexity.BaseURL, os.Getenv("PERPLEXITY_BASE_URL"))
	cfg.Perplexity.Model = envOr(cfg.Perplexity.Model, os.Getenv("PERPLEXITY_MODEL"))

	cfg.OpenRouter.APIKey = envOr(cfg.OpenRouter.APIKey, os.Getenv("OPENROUTER_API_KEY"))
	cfg.OpenRouter.BaseURL = envOr(cfg.OpenRouter.BaseURL, os.Getenv("OPENROUTER_BASE_URL"))
	cfg.OpenRouter.Model = envOr(cfg.OpenRouter.Model, os.Getenv("OPENROUTER_MODEL"))

	return cfg.WithDefaults()
}

// ApplyEnvDefaults fills empty config fields from environment variables.
func ApplyEnvDefaults(cfg *Config) *Config {
	if cfg == nil {
		return ConfigFromEnv()
	}
	providerSet := strings.TrimSpace(cfg.Provider) != ""
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

	if current.Brave.APIKey == "" {
		current.Brave.APIKey = envCfg.Brave.APIKey
	}
	if current.Brave.BaseURL == "" {
		current.Brave.BaseURL = envCfg.Brave.BaseURL
	}

	if current.Perplexity.APIKey == "" {
		current.Perplexity.APIKey = envCfg.Perplexity.APIKey
	}
	if current.Perplexity.BaseURL == "" {
		current.Perplexity.BaseURL = envCfg.Perplexity.BaseURL
	}
	if current.Perplexity.Model == "" {
		current.Perplexity.Model = envCfg.Perplexity.Model
	}

	if current.OpenRouter.APIKey == "" {
		current.OpenRouter.APIKey = envCfg.OpenRouter.APIKey
	}
	if current.OpenRouter.BaseURL == "" {
		current.OpenRouter.BaseURL = envCfg.OpenRouter.BaseURL
	}
	if current.OpenRouter.Model == "" {
		current.OpenRouter.Model = envCfg.OpenRouter.Model
	}

	if !providerSet && strings.TrimSpace(current.Exa.APIKey) != "" {
		current.Provider = ProviderExa
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
