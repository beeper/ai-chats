package search

import (
	"slices"
	"strings"
)

const (
	ProviderExa        = "exa"
	DefaultSearchCount = 5
	MaxSearchCount     = 10
	DefaultTimeoutSecs = 30
	DefaultCacheTtlSecs = 900
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
	if strings.TrimSpace(c.Provider) == "" {
		c.Provider = ProviderExa
	}
	if len(c.Fallbacks) == 0 {
		c.Fallbacks = slices.Clone(DefaultFallbackOrder)
	}
	c.Exa = c.Exa.withDefaults()
	return c
}

func (c ExaConfig) withDefaults() ExaConfig {
	if c.BaseURL == "" {
		c.BaseURL = "https://api.exa.ai"
	}
	if c.Type == "" {
		c.Type = "auto"
	}
	if c.NumResults <= 0 {
		c.NumResults = DefaultSearchCount
	}
	if c.TextMaxCharacters <= 0 {
		c.TextMaxCharacters = 500
	}
	c.Highlights = true
	return c
}

func isEnabled(flag *bool, fallback bool) bool {
	if flag == nil {
		return fallback
	}
	return *flag
}
