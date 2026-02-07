package fetch

import (
	"slices"
	"strings"
)

const (
	ProviderExa        = "exa"
	ProviderDirect     = "direct"
	DefaultTimeoutSecs = 30
	DefaultMaxChars    = 50_000
)

var DefaultFallbackOrder = []string{
	ProviderExa,
	ProviderDirect,
}

// Config controls fetch provider selection and credentials.
type Config struct {
	Provider  string   `yaml:"provider"`
	Fallbacks []string `yaml:"fallbacks"`

	Exa    ExaConfig    `yaml:"exa"`
	Direct DirectConfig `yaml:"direct"`
}

type ExaConfig struct {
	Enabled           *bool  `yaml:"enabled"`
	BaseURL           string `yaml:"base_url"`
	APIKey            string `yaml:"api_key"`
	IncludeText       bool   `yaml:"include_text"`
	TextMaxCharacters int    `yaml:"text_max_chars"`
}

type DirectConfig struct {
	Enabled      *bool  `yaml:"enabled"`
	TimeoutSecs  int    `yaml:"timeout_seconds"`
	UserAgent    string `yaml:"user_agent"`
	Readability  bool   `yaml:"readability"`
	MaxChars     int    `yaml:"max_chars"`
	MaxRedirects int    `yaml:"max_redirects"`
	CacheTtlSecs int    `yaml:"cache_ttl_seconds"`
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
	c.Direct = c.Direct.withDefaults()
	return c
}

func (c ExaConfig) withDefaults() ExaConfig {
	if c.BaseURL == "" {
		c.BaseURL = "https://api.exa.ai"
	}
	if c.TextMaxCharacters <= 0 {
		c.TextMaxCharacters = 5_000
	}
	return c
}

func (c DirectConfig) withDefaults() DirectConfig {
	if c.TimeoutSecs <= 0 {
		c.TimeoutSecs = DefaultTimeoutSecs
	}
	if c.UserAgent == "" {
		c.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_7_2) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
	}
	if c.MaxChars <= 0 {
		c.MaxChars = DefaultMaxChars
	}
	if c.MaxRedirects <= 0 {
		c.MaxRedirects = 3
	}
	return c
}

func isEnabled(flag *bool, fallback bool) bool {
	if flag == nil {
		return fallback
	}
	return *flag
}
