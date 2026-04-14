package retrieval

import (
	"github.com/beeper/agentremote/pkg/shared/exa"
)

const (
	ProviderExa        = "exa"
	ProviderDirect     = "direct"
	DefaultSearchCount = 5
	MaxSearchCount     = 10
	DefaultTimeoutSecs = 30
	DefaultMaxChars    = 50_000
)

var (
	DefaultSearchFallbackOrder = []string{ProviderExa}
	DefaultFetchFallbackOrder  = []string{ProviderExa, ProviderDirect}
)

// SearchConfig controls search provider selection and credentials.
type SearchConfig struct {
	Provider  string   `yaml:"provider"`
	Fallbacks []string `yaml:"fallbacks"`

	Exa ExaConfig `yaml:"exa"`
}

// FetchConfig controls fetch provider selection and credentials.
type FetchConfig struct {
	Provider  string   `yaml:"provider"`
	Fallbacks []string `yaml:"fallbacks"`

	Exa    ExaConfig    `yaml:"exa"`
	Direct DirectConfig `yaml:"direct"`
}

// ExaConfig configures the Exa provider for both search and fetch.
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

// DirectConfig configures the direct fetch provider.
type DirectConfig struct {
	Enabled      *bool  `yaml:"enabled"`
	TimeoutSecs  int    `yaml:"timeout_seconds"`
	UserAgent    string `yaml:"user_agent"`
	Readability  bool   `yaml:"readability"`
	MaxChars     int    `yaml:"max_chars"`
	MaxRedirects int    `yaml:"max_redirects"`
	CacheTtlSecs int    `yaml:"cache_ttl_seconds"`
}

func (c *SearchConfig) WithDefaults() *SearchConfig {
	if c == nil {
		c = &SearchConfig{}
	}
	if c.Provider == "" {
		c.Provider = ProviderExa
	}
	if len(c.Fallbacks) == 0 {
		c.Fallbacks = append([]string(nil), DefaultSearchFallbackOrder...)
	}
	c.Exa = c.Exa.withSearchDefaults()
	return c
}

func (c *FetchConfig) WithDefaults() *FetchConfig {
	if c == nil {
		c = &FetchConfig{}
	}
	if c.Provider == "" {
		c.Provider = ProviderExa
	}
	if len(c.Fallbacks) == 0 {
		c.Fallbacks = append([]string(nil), DefaultFetchFallbackOrder...)
	}
	c.Exa = c.Exa.withFetchDefaults()
	c.Direct = c.Direct.withDefaults()
	return c
}

func (c ExaConfig) withSearchDefaults() ExaConfig {
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

func (c ExaConfig) withFetchDefaults() ExaConfig {
	exa.ApplyConfigDefaults(&c.BaseURL, &c.TextMaxCharacters, 5_000)
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
