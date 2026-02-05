package connector

import (
	_ "embed"
	"time"

	"go.mau.fi/util/configupgrade"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
)

//go:embed example-config.yaml
var exampleNetworkConfig string

// Config represents the connector-specific configuration that is nested under
// the `network:` block in the main bridge config.
type Config struct {
	Beeper     BeeperConfig                       `yaml:"beeper"`
	Providers  ProvidersConfig                    `yaml:"providers"`
	Models     *ModelsConfig                      `yaml:"models"`
	Bridge     BridgeConfig                       `yaml:"bridge"`
	Tools      ToolProvidersConfig                `yaml:"tools"`
	ToolPolicy *toolpolicy.GlobalToolPolicyConfig `yaml:"tool_policy"`
	Agents     *AgentsConfig                      `yaml:"agents"`
	Channels   *ChannelsConfig                    `yaml:"channels"`
	Cron       *CronConfig                        `yaml:"cron"`
	Messages   *MessagesConfig                    `yaml:"messages"`
	Session    *SessionConfig                     `yaml:"session"`

	// Global settings
	DefaultSystemPrompt string              `yaml:"default_system_prompt"`
	ModelCacheDuration  time.Duration       `yaml:"model_cache_duration"`
	Memory              *MemoryConfig       `yaml:"memory"`
	MemorySearch        *MemorySearchConfig `yaml:"memory_search"`

	// Context pruning configuration (OpenClaw-style)
	Pruning *PruningConfig `yaml:"pruning"`

	// Link preview configuration
	LinkPreviews *LinkPreviewConfig `yaml:"link_previews"`

	// Inbound message processing configuration
	Inbound *InboundConfig `yaml:"inbound"`
}

// AgentsConfig configures agent defaults (OpenClaw-style).
type AgentsConfig struct {
	Defaults *AgentDefaultsConfig `yaml:"defaults"`
	List     []AgentEntryConfig   `yaml:"list"`
}

// AgentDefaultsConfig defines default agent settings.
type AgentDefaultsConfig struct {
	Subagents         *agents.SubagentConfig `yaml:"subagents"`
	SkipBootstrap     bool                   `yaml:"skip_bootstrap"`
	BootstrapMaxChars int                    `yaml:"bootstrap_max_chars"`
	SoulEvil          *agents.SoulEvilConfig `yaml:"soul_evil"`
	Heartbeat         *HeartbeatConfig       `yaml:"heartbeat"`
	UserTimezone      string                 `yaml:"userTimezone"`
	EnvelopeTimezone  string                 `yaml:"envelopeTimezone"`  // local|utc|user|IANA
	EnvelopeTimestamp string                 `yaml:"envelopeTimestamp"` // on|off
	EnvelopeElapsed   string                 `yaml:"envelopeElapsed"`   // on|off
}

// AgentEntryConfig defines per-agent overrides (OpenClaw-style).
type AgentEntryConfig struct {
	ID        string           `yaml:"id"`
	Heartbeat *HeartbeatConfig `yaml:"heartbeat"`
}

// HeartbeatConfig configures periodic heartbeat runs (OpenClaw-style).
type HeartbeatConfig struct {
	Every            *string                     `yaml:"every"`
	ActiveHours      *HeartbeatActiveHoursConfig `yaml:"activeHours"`
	Model            *string                     `yaml:"model"`
	Session          *string                     `yaml:"session"`
	Target           *string                     `yaml:"target"`
	To               *string                     `yaml:"to"`
	Prompt           *string                     `yaml:"prompt"`
	AckMaxChars      *int                        `yaml:"ackMaxChars"`
	IncludeReasoning *bool                       `yaml:"includeReasoning"`
}

type HeartbeatActiveHoursConfig struct {
	Start    string `yaml:"start"`
	End      string `yaml:"end"`
	Timezone string `yaml:"timezone"`
}

// CronConfig configures cron scheduling (OpenClaw-style).
type CronConfig struct {
	Enabled           *bool  `yaml:"enabled"`
	Store             string `yaml:"store"`
	MaxConcurrentRuns int    `yaml:"maxConcurrentRuns"`
}

// ChannelsConfig defines per-channel settings (limited to heartbeat visibility for Matrix).
type ChannelsConfig struct {
	Defaults *ChannelDefaultsConfig `yaml:"defaults"`
	Matrix   *ChannelConfig         `yaml:"matrix"`
}

type ChannelDefaultsConfig struct {
	Heartbeat *ChannelHeartbeatVisibilityConfig `yaml:"heartbeat"`
}

type ChannelConfig struct {
	Heartbeat *ChannelHeartbeatVisibilityConfig `yaml:"heartbeat"`
}

type ChannelHeartbeatVisibilityConfig struct {
	ShowOk       *bool `yaml:"showOk"`
	ShowAlerts   *bool `yaml:"showAlerts"`
	UseIndicator *bool `yaml:"useIndicator"`
}

// MessagesConfig defines message rendering settings (OpenClaw-style).
type MessagesConfig struct {
	ResponsePrefix   string                 `yaml:"responsePrefix"`
	AckReaction      string                 `yaml:"ackReaction"`
	AckReactionScope string                 `yaml:"ackReactionScope"` // group-mentions|group-all|direct|all|off|none
	RemoveAckAfter   bool                   `yaml:"removeAckAfterReply"`
	GroupChat        *GroupChatConfig       `yaml:"groupChat"`
	InboundDebounce  *InboundDebounceConfig `yaml:"inbound"`
}

// GroupChatConfig mirrors OpenClaw's group chat settings.
type GroupChatConfig struct {
	MentionPatterns []string `yaml:"mentionPatterns"`
}

// InboundDebounceConfig mirrors OpenClaw's inbound debounce config.
type InboundDebounceConfig struct {
	DebounceMs int            `yaml:"debounceMs"`
	ByChannel  map[string]int `yaml:"byChannel"`
}

// SessionConfig configures session store behavior (OpenClaw-style).
type SessionConfig struct {
	Scope   string `yaml:"scope"`
	MainKey string `yaml:"mainKey"`
	Store   string `yaml:"store"`
}

// MemoryConfig configures memory behavior (OpenClaw-style).
type MemoryConfig struct {
	Citations string `yaml:"citations"`
}

// MemorySearchConfig configures semantic memory search (OpenClaw-style).
type MemorySearchConfig struct {
	Enabled      *bool                           `yaml:"enabled"`
	Sources      []string                        `yaml:"sources"`
	ExtraPaths   []string                        `yaml:"extra_paths"`
	Provider     string                          `yaml:"provider"`
	Model        string                          `yaml:"model"`
	Remote       *MemorySearchRemoteConfig       `yaml:"remote"`
	Fallback     string                          `yaml:"fallback"`
	Local        *MemorySearchLocalConfig        `yaml:"local"`
	Store        *MemorySearchStoreConfig        `yaml:"store"`
	Chunking     *MemorySearchChunkingConfig     `yaml:"chunking"`
	Sync         *MemorySearchSyncConfig         `yaml:"sync"`
	Query        *MemorySearchQueryConfig        `yaml:"query"`
	Cache        *MemorySearchCacheConfig        `yaml:"cache"`
	Experimental *MemorySearchExperimentalConfig `yaml:"experimental"`
}

type MemorySearchRemoteConfig struct {
	BaseURL string                   `yaml:"base_url"`
	APIKey  string                   `yaml:"api_key"`
	Headers map[string]string        `yaml:"headers"`
	Batch   *MemorySearchBatchConfig `yaml:"batch"`
}

type MemorySearchBatchConfig struct {
	Enabled        *bool `yaml:"enabled"`
	Wait           *bool `yaml:"wait"`
	Concurrency    int   `yaml:"concurrency"`
	PollIntervalMs int   `yaml:"poll_interval_ms"`
	TimeoutMinutes int   `yaml:"timeout_minutes"`
}

type MemorySearchLocalConfig struct {
	ModelPath     string `yaml:"model_path"`
	ModelCacheDir string `yaml:"model_cache_dir"`
	BaseURL       string `yaml:"base_url"`
	APIKey        string `yaml:"api_key"`
}

type MemorySearchStoreConfig struct {
	Driver string                    `yaml:"driver"`
	Path   string                    `yaml:"path"`
	Vector *MemorySearchVectorConfig `yaml:"vector"`
}

type MemorySearchVectorConfig struct {
	Enabled       *bool  `yaml:"enabled"`
	ExtensionPath string `yaml:"extension_path"`
}

type MemorySearchChunkingConfig struct {
	Tokens  int `yaml:"tokens"`
	Overlap int `yaml:"overlap"`
}

type MemorySearchSyncConfig struct {
	OnSessionStart  *bool                          `yaml:"on_session_start"`
	OnSearch        *bool                          `yaml:"on_search"`
	Watch           *bool                          `yaml:"watch"`
	WatchDebounceMs int                            `yaml:"watch_debounce_ms"`
	IntervalMinutes int                            `yaml:"interval_minutes"`
	Sessions        *MemorySearchSessionSyncConfig `yaml:"sessions"`
}

type MemorySearchSessionSyncConfig struct {
	DeltaBytes    int `yaml:"delta_bytes"`
	DeltaMessages int `yaml:"delta_messages"`
}

type MemorySearchQueryConfig struct {
	MaxResults int                       `yaml:"max_results"`
	MinScore   float64                   `yaml:"min_score"`
	Hybrid     *MemorySearchHybridConfig `yaml:"hybrid"`
}

type MemorySearchHybridConfig struct {
	Enabled             *bool   `yaml:"enabled"`
	VectorWeight        float64 `yaml:"vector_weight"`
	TextWeight          float64 `yaml:"text_weight"`
	CandidateMultiplier int     `yaml:"candidate_multiplier"`
}

type MemorySearchCacheConfig struct {
	Enabled    *bool `yaml:"enabled"`
	MaxEntries int   `yaml:"max_entries"`
}

type MemorySearchExperimentalConfig struct {
	SessionMemory *bool `yaml:"session_memory"`
}

// ToolProvidersConfig configures external tool providers like search and fetch.
type ToolProvidersConfig struct {
	Search *SearchConfig     `yaml:"search"`
	Fetch  *FetchConfig      `yaml:"fetch"`
	Media  *MediaToolsConfig `yaml:"media"`
}

// MediaUnderstandingScopeMatch defines match criteria for media understanding scope rules.
type MediaUnderstandingScopeMatch struct {
	Channel   string `yaml:"channel"`
	ChatType  string `yaml:"chatType"`
	KeyPrefix string `yaml:"keyPrefix"`
}

// MediaUnderstandingScopeRule defines a single allow/deny rule.
type MediaUnderstandingScopeRule struct {
	Action string                        `yaml:"action"`
	Match  *MediaUnderstandingScopeMatch `yaml:"match"`
}

// MediaUnderstandingScopeConfig controls allow/deny gating for media understanding.
type MediaUnderstandingScopeConfig struct {
	Default string                        `yaml:"default"`
	Rules   []MediaUnderstandingScopeRule `yaml:"rules"`
}

// MediaUnderstandingAttachmentsConfig controls how media attachments are selected.
type MediaUnderstandingAttachmentsConfig struct {
	Mode           string `yaml:"mode"`
	MaxAttachments int    `yaml:"maxAttachments"`
	Prefer         string `yaml:"prefer"`
}

// MediaUnderstandingDeepgramConfig is a deprecated compatibility shim for Deepgram settings.
type MediaUnderstandingDeepgramConfig struct {
	DetectLanguage *bool `yaml:"detectLanguage"`
	Punctuate      *bool `yaml:"punctuate"`
	SmartFormat    *bool `yaml:"smartFormat"`
}

// MediaUnderstandingModelConfig defines a single media understanding model entry.
type MediaUnderstandingModelConfig struct {
	Provider         string                            `yaml:"provider"`
	Model            string                            `yaml:"model"`
	Capabilities     []string                          `yaml:"capabilities"`
	Type             string                            `yaml:"type"`
	Command          string                            `yaml:"command"`
	Args             []string                          `yaml:"args"`
	Prompt           string                            `yaml:"prompt"`
	MaxChars         int                               `yaml:"maxChars"`
	MaxBytes         int                               `yaml:"maxBytes"`
	TimeoutSeconds   int                               `yaml:"timeoutSeconds"`
	Language         string                            `yaml:"language"`
	ProviderOptions  map[string]map[string]any         `yaml:"providerOptions"`
	Deepgram         *MediaUnderstandingDeepgramConfig `yaml:"deepgram"`
	BaseURL          string                            `yaml:"baseUrl"`
	Headers          map[string]string                 `yaml:"headers"`
	Profile          string                            `yaml:"profile"`
	PreferredProfile string                            `yaml:"preferredProfile"`
}

// MediaUnderstandingConfig defines defaults for media understanding of a capability.
type MediaUnderstandingConfig struct {
	Enabled         *bool                                `yaml:"enabled"`
	Scope           *MediaUnderstandingScopeConfig       `yaml:"scope"`
	MaxBytes        int                                  `yaml:"maxBytes"`
	MaxChars        int                                  `yaml:"maxChars"`
	Prompt          string                               `yaml:"prompt"`
	TimeoutSeconds  int                                  `yaml:"timeoutSeconds"`
	Language        string                               `yaml:"language"`
	ProviderOptions map[string]map[string]any            `yaml:"providerOptions"`
	Deepgram        *MediaUnderstandingDeepgramConfig    `yaml:"deepgram"`
	BaseURL         string                               `yaml:"baseUrl"`
	Headers         map[string]string                    `yaml:"headers"`
	Attachments     *MediaUnderstandingAttachmentsConfig `yaml:"attachments"`
	Models          []MediaUnderstandingModelConfig      `yaml:"models"`
}

// MediaToolsConfig configures media understanding/transcription.
type MediaToolsConfig struct {
	Models      []MediaUnderstandingModelConfig `yaml:"models"`
	Concurrency int                             `yaml:"concurrency"`
	Image       *MediaUnderstandingConfig       `yaml:"image"`
	Audio       *MediaUnderstandingConfig       `yaml:"audio"`
	Video       *MediaUnderstandingConfig       `yaml:"video"`
}

type SearchConfig struct {
	Provider  string   `yaml:"provider"`
	Fallbacks []string `yaml:"fallbacks"`

	Exa        ProviderExaConfig        `yaml:"exa"`
	Brave      ProviderBraveConfig      `yaml:"brave"`
	Perplexity ProviderPerplexityConfig `yaml:"perplexity"`
	OpenRouter ProviderOpenRouterConfig `yaml:"openrouter"`
	DDG        ProviderDDGConfig        `yaml:"ddg"`
}

type FetchConfig struct {
	Provider  string   `yaml:"provider"`
	Fallbacks []string `yaml:"fallbacks"`

	Exa    ProviderExaConfig    `yaml:"exa"`
	Direct ProviderDirectConfig `yaml:"direct"`
}

type ProviderExaConfig struct {
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

type ProviderBraveConfig struct {
	Enabled          *bool  `yaml:"enabled"`
	BaseURL          string `yaml:"base_url"`
	APIKey           string `yaml:"api_key"`
	TimeoutSecs      int    `yaml:"timeout_seconds"`
	CacheTtlSecs     int    `yaml:"cache_ttl_seconds"`
	SearchLang       string `yaml:"search_lang"`
	UILang           string `yaml:"ui_lang"`
	DefaultCountry   string `yaml:"default_country"`
	DefaultFreshness string `yaml:"default_freshness"`
}

type ProviderPerplexityConfig struct {
	Enabled      *bool  `yaml:"enabled"`
	APIKey       string `yaml:"api_key"`
	BaseURL      string `yaml:"base_url"`
	Model        string `yaml:"model"`
	TimeoutSecs  int    `yaml:"timeout_seconds"`
	CacheTtlSecs int    `yaml:"cache_ttl_seconds"`
}

type ProviderOpenRouterConfig struct {
	Enabled      *bool  `yaml:"enabled"`
	APIKey       string `yaml:"api_key"`
	BaseURL      string `yaml:"base_url"`
	Model        string `yaml:"model"`
	TimeoutSecs  int    `yaml:"timeout_seconds"`
	CacheTtlSecs int    `yaml:"cache_ttl_seconds"`
}

type ProviderDDGConfig struct {
	Enabled     *bool `yaml:"enabled"`
	TimeoutSecs int   `yaml:"timeout_seconds"`
}

type ProviderDirectConfig struct {
	Enabled      *bool  `yaml:"enabled"`
	TimeoutSecs  int    `yaml:"timeout_seconds"`
	UserAgent    string `yaml:"user_agent"`
	Readability  bool   `yaml:"readability"`
	MaxChars     int    `yaml:"max_chars"`
	MaxRedirects int    `yaml:"max_redirects"`
	CacheTtlSecs int    `yaml:"cache_ttl_seconds"`
}

// InboundConfig contains settings for inbound message processing
// including deduplication and debouncing.
type InboundConfig struct {
	// Deduplication settings
	DedupeTTL     time.Duration `yaml:"dedupe_ttl"`      // Time-to-live for dedupe entries (default: 20m)
	DedupeMaxSize int           `yaml:"dedupe_max_size"` // Max entries in dedupe cache (default: 5000)

	// Debounce settings
	DefaultDebounceMs int `yaml:"default_debounce_ms"` // Default debounce delay in ms (default: 500)
}

// WithDefaults returns the InboundConfig with default values applied.
func (c *InboundConfig) WithDefaults() *InboundConfig {
	if c == nil {
		c = &InboundConfig{}
	}
	if c.DedupeTTL <= 0 {
		c.DedupeTTL = DefaultDedupeTTL
	}
	if c.DedupeMaxSize <= 0 {
		c.DedupeMaxSize = DefaultDedupeMaxSize
	}
	if c.DefaultDebounceMs <= 0 {
		c.DefaultDebounceMs = DefaultDebounceMs
	}
	return c
}

// BeeperConfig contains Beeper AI proxy credentials for automatic login.
// If both BaseURL and Token are set, users don't need to manually log in.
type BeeperConfig struct {
	BaseURL string `yaml:"base_url"` // Beeper AI proxy endpoint
	Token   string `yaml:"token"`    // Beeper Matrix access token
}

// ProviderConfig holds settings for a specific AI provider.
type ProviderConfig struct {
	APIKey           string `yaml:"api_key"`
	BaseURL          string `yaml:"base_url"`
	DefaultModel     string `yaml:"default_model"`
	DefaultPDFEngine string `yaml:"default_pdf_engine"` // pdf-text, mistral-ocr (default), native
}

// ProvidersConfig contains per-provider configuration.
type ProvidersConfig struct {
	Beeper     ProviderConfig `yaml:"beeper"`
	OpenAI     ProviderConfig `yaml:"openai"`
	OpenRouter ProviderConfig `yaml:"openrouter"`
}

// ModelsConfig configures model catalog seeding (OpenClaw-style).
type ModelsConfig struct {
	Mode      string                         `yaml:"mode"` // merge | replace
	Providers map[string]ModelProviderConfig `yaml:"providers"`
}

// ModelProviderConfig describes models for a specific provider.
type ModelProviderConfig struct {
	Models []ModelDefinitionConfig `yaml:"models"`
}

// ModelDefinitionConfig defines a model entry for catalog seeding.
type ModelDefinitionConfig struct {
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	Reasoning     bool     `yaml:"reasoning"`
	Input         []string `yaml:"input"`
	ContextWindow int      `yaml:"context_window"`
	MaxTokens     int      `yaml:"max_tokens"`
}

// BridgeConfig tweaks Matrix-side behaviour for the AI bridge.
type BridgeConfig struct {
	CommandPrefix string `yaml:"command_prefix"`
}

func upgradeConfig(helper configupgrade.Helper) {
	// Beeper credentials for auto-login
	helper.Copy(configupgrade.Str, "beeper", "base_url")
	helper.Copy(configupgrade.Str, "beeper", "token")

	// Per-provider default models
	helper.Copy(configupgrade.Str, "providers", "beeper", "default_model")
	helper.Copy(configupgrade.Str, "providers", "beeper", "default_pdf_engine")
	helper.Copy(configupgrade.Str, "providers", "openai", "api_key")
	helper.Copy(configupgrade.Str, "providers", "openai", "base_url")
	helper.Copy(configupgrade.Str, "providers", "openai", "default_model")
	helper.Copy(configupgrade.Str, "providers", "openrouter", "api_key")
	helper.Copy(configupgrade.Str, "providers", "openrouter", "base_url")
	helper.Copy(configupgrade.Str, "providers", "openrouter", "default_model")
	helper.Copy(configupgrade.Str, "providers", "openrouter", "default_pdf_engine")

	// Global settings
	helper.Copy(configupgrade.Str, "default_system_prompt")
	helper.Copy(configupgrade.Str, "model_cache_duration")

	// Bridge-specific configuration
	helper.Copy(configupgrade.Str, "bridge", "command_prefix")

	// Context pruning configuration
	helper.Copy(configupgrade.Bool, "pruning", "enabled")
	helper.Copy(configupgrade.Float, "pruning", "soft_trim_ratio")
	helper.Copy(configupgrade.Float, "pruning", "hard_clear_ratio")
	helper.Copy(configupgrade.Int, "pruning", "keep_last_assistants")
	helper.Copy(configupgrade.Int, "pruning", "min_prunable_chars")
	helper.Copy(configupgrade.Int, "pruning", "soft_trim_max_chars")
	helper.Copy(configupgrade.Int, "pruning", "soft_trim_head_chars")
	helper.Copy(configupgrade.Int, "pruning", "soft_trim_tail_chars")
	helper.Copy(configupgrade.Bool, "pruning", "hard_clear_enabled")
	helper.Copy(configupgrade.Str, "pruning", "hard_clear_placeholder")

	// Compaction configuration (LLM summarization)
	helper.Copy(configupgrade.Bool, "pruning", "summarization_enabled")
	helper.Copy(configupgrade.Str, "pruning", "summarization_model")
	helper.Copy(configupgrade.Int, "pruning", "max_summary_tokens")
	helper.Copy(configupgrade.Float, "pruning", "max_history_share")
	helper.Copy(configupgrade.Int, "pruning", "reserve_tokens")
	helper.Copy(configupgrade.Str, "pruning", "custom_instructions")
	helper.Copy(configupgrade.Bool, "pruning", "memory_flush", "enabled")
	helper.Copy(configupgrade.Int, "pruning", "memory_flush", "soft_threshold_tokens")
	helper.Copy(configupgrade.Str, "pruning", "memory_flush", "prompt")
	helper.Copy(configupgrade.Str, "pruning", "memory_flush", "system_prompt")

	// Link preview configuration
	helper.Copy(configupgrade.Bool, "link_previews", "enabled")
	helper.Copy(configupgrade.Int, "link_previews", "max_urls_inbound")
	helper.Copy(configupgrade.Int, "link_previews", "max_urls_outbound")
	helper.Copy(configupgrade.Str, "link_previews", "fetch_timeout")
	helper.Copy(configupgrade.Int, "link_previews", "max_content_chars")
	helper.Copy(configupgrade.Int, "link_previews", "max_page_bytes")
	helper.Copy(configupgrade.Int, "link_previews", "max_image_bytes")
	helper.Copy(configupgrade.Str, "link_previews", "cache_ttl")

	// Inbound message processing configuration
	helper.Copy(configupgrade.Str, "inbound", "dedupe_ttl")
	helper.Copy(configupgrade.Int, "inbound", "dedupe_max_size")
	helper.Copy(configupgrade.Int, "inbound", "default_debounce_ms")

	// Cron configuration
	helper.Copy(configupgrade.Bool, "cron", "enabled")
	helper.Copy(configupgrade.Str, "cron", "store")
	helper.Copy(configupgrade.Int, "cron", "maxConcurrentRuns")

	// Messages configuration
	helper.Copy(configupgrade.Str, "messages", "responsePrefix")

	// Session configuration (OpenClaw-style)
	helper.Copy(configupgrade.Str, "session", "scope")
	helper.Copy(configupgrade.Str, "session", "mainKey")
	helper.Copy(configupgrade.Str, "session", "store")

	// Agents heartbeat configuration
	helper.Copy(configupgrade.Str, "agents", "defaults", "heartbeat", "every")
	helper.Copy(configupgrade.Str, "agents", "defaults", "heartbeat", "prompt")
	helper.Copy(configupgrade.Str, "agents", "defaults", "heartbeat", "model")
	helper.Copy(configupgrade.Str, "agents", "defaults", "heartbeat", "session")
	helper.Copy(configupgrade.Str, "agents", "defaults", "heartbeat", "target")
	helper.Copy(configupgrade.Str, "agents", "defaults", "heartbeat", "to")
	helper.Copy(configupgrade.Int, "agents", "defaults", "heartbeat", "ackMaxChars")
	helper.Copy(configupgrade.Bool, "agents", "defaults", "heartbeat", "includeReasoning")
	helper.Copy(configupgrade.Str, "agents", "defaults", "heartbeat", "activeHours", "start")
	helper.Copy(configupgrade.Str, "agents", "defaults", "heartbeat", "activeHours", "end")
	helper.Copy(configupgrade.Str, "agents", "defaults", "heartbeat", "activeHours", "timezone")
	helper.Copy(configupgrade.List, "agents", "list")

	// Channels heartbeat visibility
	helper.Copy(configupgrade.Bool, "channels", "defaults", "heartbeat", "showOk")
	helper.Copy(configupgrade.Bool, "channels", "defaults", "heartbeat", "showAlerts")
	helper.Copy(configupgrade.Bool, "channels", "defaults", "heartbeat", "useIndicator")
	helper.Copy(configupgrade.Bool, "channels", "matrix", "heartbeat", "showOk")
	helper.Copy(configupgrade.Bool, "channels", "matrix", "heartbeat", "showAlerts")
	helper.Copy(configupgrade.Bool, "channels", "matrix", "heartbeat", "useIndicator")

	// Tools (search + fetch)
	helper.Copy(configupgrade.Str, "tools", "search", "provider")
	helper.Copy(configupgrade.List, "tools", "search", "fallbacks")
	helper.Copy(configupgrade.Bool, "tools", "search", "exa", "enabled")
	helper.Copy(configupgrade.Str, "tools", "search", "exa", "base_url")
	helper.Copy(configupgrade.Str, "tools", "search", "exa", "api_key")
	helper.Copy(configupgrade.Str, "tools", "search", "exa", "type")
	helper.Copy(configupgrade.Str, "tools", "search", "exa", "category")
	helper.Copy(configupgrade.Int, "tools", "search", "exa", "num_results")
	helper.Copy(configupgrade.Bool, "tools", "search", "exa", "include_text")
	helper.Copy(configupgrade.Int, "tools", "search", "exa", "text_max_chars")
	helper.Copy(configupgrade.Bool, "tools", "search", "exa", "highlights")
	helper.Copy(configupgrade.Bool, "tools", "search", "brave", "enabled")
	helper.Copy(configupgrade.Str, "tools", "search", "brave", "base_url")
	helper.Copy(configupgrade.Str, "tools", "search", "brave", "api_key")
	helper.Copy(configupgrade.Int, "tools", "search", "brave", "timeout_seconds")
	helper.Copy(configupgrade.Int, "tools", "search", "brave", "cache_ttl_seconds")
	helper.Copy(configupgrade.Str, "tools", "search", "brave", "search_lang")
	helper.Copy(configupgrade.Str, "tools", "search", "brave", "ui_lang")
	helper.Copy(configupgrade.Str, "tools", "search", "brave", "default_country")
	helper.Copy(configupgrade.Str, "tools", "search", "brave", "default_freshness")
	helper.Copy(configupgrade.Bool, "tools", "search", "perplexity", "enabled")
	helper.Copy(configupgrade.Str, "tools", "search", "perplexity", "api_key")
	helper.Copy(configupgrade.Str, "tools", "search", "perplexity", "base_url")
	helper.Copy(configupgrade.Str, "tools", "search", "perplexity", "model")
	helper.Copy(configupgrade.Int, "tools", "search", "perplexity", "timeout_seconds")
	helper.Copy(configupgrade.Int, "tools", "search", "perplexity", "cache_ttl_seconds")
	helper.Copy(configupgrade.Bool, "tools", "search", "openrouter", "enabled")
	helper.Copy(configupgrade.Str, "tools", "search", "openrouter", "api_key")
	helper.Copy(configupgrade.Str, "tools", "search", "openrouter", "base_url")
	helper.Copy(configupgrade.Str, "tools", "search", "openrouter", "model")
	helper.Copy(configupgrade.Int, "tools", "search", "openrouter", "timeout_seconds")
	helper.Copy(configupgrade.Int, "tools", "search", "openrouter", "cache_ttl_seconds")
	helper.Copy(configupgrade.Bool, "tools", "search", "ddg", "enabled")
	helper.Copy(configupgrade.Int, "tools", "search", "ddg", "timeout_seconds")

	helper.Copy(configupgrade.Str, "tools", "fetch", "provider")
	helper.Copy(configupgrade.List, "tools", "fetch", "fallbacks")
	helper.Copy(configupgrade.Bool, "tools", "fetch", "exa", "enabled")
	helper.Copy(configupgrade.Str, "tools", "fetch", "exa", "base_url")
	helper.Copy(configupgrade.Str, "tools", "fetch", "exa", "api_key")
	helper.Copy(configupgrade.Bool, "tools", "fetch", "exa", "include_text")
	helper.Copy(configupgrade.Int, "tools", "fetch", "exa", "text_max_chars")
	helper.Copy(configupgrade.Bool, "tools", "fetch", "direct", "enabled")
	helper.Copy(configupgrade.Int, "tools", "fetch", "direct", "timeout_seconds")
	helper.Copy(configupgrade.Str, "tools", "fetch", "direct", "user_agent")
	helper.Copy(configupgrade.Bool, "tools", "fetch", "direct", "readability")
	helper.Copy(configupgrade.Int, "tools", "fetch", "direct", "max_chars")
	helper.Copy(configupgrade.Int, "tools", "fetch", "direct", "max_redirects")
	helper.Copy(configupgrade.Int, "tools", "fetch", "direct", "cache_ttl_seconds")

	// Memory search configuration
	helper.Copy(configupgrade.Bool, "memory_search", "enabled")
	helper.Copy(configupgrade.List, "memory_search", "sources")
	helper.Copy(configupgrade.List, "memory_search", "extra_paths")
	helper.Copy(configupgrade.Str, "memory_search", "provider")
	helper.Copy(configupgrade.Str, "memory_search", "model")
	helper.Copy(configupgrade.Str, "memory_search", "fallback")
	helper.Copy(configupgrade.Str, "memory_search", "remote", "base_url")
	helper.Copy(configupgrade.Str, "memory_search", "remote", "api_key")
	helper.Copy(configupgrade.Map, "memory_search", "remote", "headers")
	helper.Copy(configupgrade.Bool, "memory_search", "remote", "batch", "enabled")
	helper.Copy(configupgrade.Bool, "memory_search", "remote", "batch", "wait")
	helper.Copy(configupgrade.Int, "memory_search", "remote", "batch", "concurrency")
	helper.Copy(configupgrade.Int, "memory_search", "remote", "batch", "poll_interval_ms")
	helper.Copy(configupgrade.Int, "memory_search", "remote", "batch", "timeout_minutes")
	helper.Copy(configupgrade.Str, "memory_search", "local", "model_path")
	helper.Copy(configupgrade.Str, "memory_search", "local", "model_cache_dir")
	helper.Copy(configupgrade.Str, "memory_search", "local", "base_url")
	helper.Copy(configupgrade.Str, "memory_search", "local", "api_key")
	helper.Copy(configupgrade.Str, "memory_search", "store", "driver")
	helper.Copy(configupgrade.Str, "memory_search", "store", "path")
	helper.Copy(configupgrade.Bool, "memory_search", "store", "vector", "enabled")
	helper.Copy(configupgrade.Str, "memory_search", "store", "vector", "extension_path")
	helper.Copy(configupgrade.Int, "memory_search", "chunking", "tokens")
	helper.Copy(configupgrade.Int, "memory_search", "chunking", "overlap")
	helper.Copy(configupgrade.Bool, "memory_search", "sync", "on_session_start")
	helper.Copy(configupgrade.Bool, "memory_search", "sync", "on_search")
	helper.Copy(configupgrade.Bool, "memory_search", "sync", "watch")
	helper.Copy(configupgrade.Int, "memory_search", "sync", "watch_debounce_ms")
	helper.Copy(configupgrade.Int, "memory_search", "sync", "interval_minutes")
	helper.Copy(configupgrade.Int, "memory_search", "sync", "sessions", "delta_bytes")
	helper.Copy(configupgrade.Int, "memory_search", "sync", "sessions", "delta_messages")
	helper.Copy(configupgrade.Int, "memory_search", "query", "max_results")
	helper.Copy(configupgrade.Float, "memory_search", "query", "min_score")
	helper.Copy(configupgrade.Bool, "memory_search", "query", "hybrid", "enabled")
	helper.Copy(configupgrade.Float, "memory_search", "query", "hybrid", "vector_weight")
	helper.Copy(configupgrade.Float, "memory_search", "query", "hybrid", "text_weight")
	helper.Copy(configupgrade.Int, "memory_search", "query", "hybrid", "candidate_multiplier")
	helper.Copy(configupgrade.Bool, "memory_search", "cache", "enabled")
	helper.Copy(configupgrade.Int, "memory_search", "cache", "max_entries")
	helper.Copy(configupgrade.Bool, "memory_search", "experimental", "session_memory")

	// Tool policy (OpenClaw-style)
	helper.Copy(configupgrade.Map, "tool_policy")
}
