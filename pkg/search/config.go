package search

import (
	"github.com/beeper/agentremote/pkg/shared/exa"
	"github.com/beeper/agentremote/pkg/shared/providerkit"
)

const (
	ProviderExa        = "exa"
	DefaultSearchCount = 5
	MaxSearchCount     = 10
	DefaultTimeoutSecs = 30
)

var DefaultFallbackOrder = []string{
	ProviderExa,
}

// Config controls search provider selection and credentials.
type Config struct {
	Provider  string   `yaml:"provider"`
	Fallbacks []string `yaml:"fallbacks"`

	Exa ExaConfig `yaml:"exa"`
}

type ExaConfig struct {
	Enabled           *bool  `yaml:"enabled"`
	BaseURL           string `yaml:"base_url"`
	APIKey            string `yaml:"api_key"`
	Type              string `yaml:"type"`
	Category          string `yaml:"category"`
	NumResults        int    `yaml:"num_results"`
	IncludeText       bool   `yaml:"include_text"`
	TextMaxCharacters int    `yaml:"text_max_chars"`
	Highlights        bool   `yaml:"highlights"`
}

func (c *Config) WithDefaults() *Config {
	if c == nil {
		c = &Config{}
	}
	providerkit.ApplyDefaults(&c.Provider, &c.Fallbacks, ProviderExa, DefaultFallbackOrder)
	c.Exa = c.Exa.withDefaults()
	return c
}

func (c ExaConfig) withDefaults() ExaConfig {
	exa.ApplyConfigDefaults(&c.BaseURL, &c.TextMaxCharacters, 500)
	if c.Type == "" {
		c.Type = "auto"
	}
	if c.NumResults <= 0 {
		c.NumResults = DefaultSearchCount
	}
	c.Highlights = true
	return c
}
