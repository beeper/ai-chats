package ai

import (
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/beeper/agentremote/pkg/shared/bridgeconfig"
)

//go:embed integrations_example-config.yaml
var exampleNetworkConfig string

// Config represents the connector-specific configuration that is nested under
// the `network:` block in the main bridge config.
type Config struct {
	Beeper   BeeperConfig        `yaml:"beeper"`
	Models   *ModelsConfig       `yaml:"models"`
	Bridge   BridgeConfig        `yaml:"bridge"`
	Tools    ToolProvidersConfig `yaml:"tools"`
	Channels *ChannelsConfig     `yaml:"channels"`
	Messages *MessagesConfig     `yaml:"messages"`
	Commands *CommandsConfig     `yaml:"commands"`

	// Global settings
	DefaultSystemPrompt string        `yaml:"default_system_prompt"`
	ModelCacheDuration  time.Duration `yaml:"model_cache_duration"`

	// Inbound message processing configuration
	Inbound *InboundConfig `yaml:"inbound"`
}

// ChannelsConfig defines per-channel settings.
type ChannelsConfig struct {
	Defaults *ChannelDefaultsConfig `yaml:"defaults"`
	Matrix   *ChannelConfig         `yaml:"matrix"`
}

type ChannelDefaultsConfig struct {
}

type ChannelConfig struct {
	ReplyToMode   string `yaml:"reply_to_mode"`  // off|first|all (Matrix)
	ThreadReplies string `yaml:"thread_replies"` // off|inbound|always (Matrix)
}

// MessagesConfig defines message rendering settings.
type MessagesConfig struct {
	AckReaction      string                 `yaml:"ack_reaction"`
	AckReactionScope string                 `yaml:"ack_reaction_scope"` // group-mentions|group-all|direct|all|off|none
	RemoveAckAfter   bool                   `yaml:"remove_ack_after"`
	GroupChat        *GroupChatConfig       `yaml:"group_chat"`
	DirectChat       *DirectChatConfig      `yaml:"direct_chat"`
	Queue            *QueueConfig           `yaml:"queue"`
	InboundDebounce  *InboundDebounceConfig `yaml:"inbound"`
}

// CommandsConfig defines command authorization settings.
type CommandsConfig struct {
	OwnerAllowFrom []string `yaml:"owner_allow_from"`
}

// GroupChatConfig defines group chat settings.
type GroupChatConfig struct {
	MentionPatterns []string `yaml:"mention_patterns"`
	Activation      string   `yaml:"activation"` // mention|always
	HistoryLimit    int      `yaml:"history_limit"`
}

// DirectChatConfig defines direct message defaults.
type DirectChatConfig struct {
	HistoryLimit int `yaml:"history_limit"`
}

// InboundDebounceConfig defines inbound debounce behavior.
type InboundDebounceConfig struct {
	DebounceMs int            `yaml:"debounce_ms"`
	ByChannel  map[string]int `yaml:"by_channel"`
}

// QueueConfig defines queue behavior.
type QueueConfig struct {
	Mode                string            `yaml:"mode"`
	ByChannel           map[string]string `yaml:"by_channel"`
	DebounceMs          *int              `yaml:"debounce_ms"`
	DebounceMsByChannel map[string]int    `yaml:"debounce_ms_by_channel"`
	Cap                 *int              `yaml:"cap"`
	Drop                string            `yaml:"drop"`
}

// ToolProvidersConfig configures external tool providers like search and fetch.
type ToolProvidersConfig struct {
	Web   *WebToolsConfig    `yaml:"web"`
	Links *LinkPreviewConfig `yaml:"links"`
	Media *MediaToolsConfig  `yaml:"media"`
}

type WebToolsConfig struct {
	Search *SearchConfig `yaml:"search"`
	Fetch  *FetchConfig  `yaml:"fetch"`
}

// MediaUnderstandingScopeMatch defines match criteria for media understanding scope rules.
type MediaUnderstandingScopeMatch struct {
	Channel   string `yaml:"channel"`
	ChatType  string `yaml:"chat_type"`
	KeyPrefix string `yaml:"key_prefix"`
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
	MaxAttachments int    `yaml:"max_attachments"`
	Prefer         string `yaml:"prefer"`
}

// MediaUnderstandingModelConfig defines a single media understanding model entry.
type MediaUnderstandingModelConfig struct {
	Provider         string                    `yaml:"provider"`
	Model            string                    `yaml:"model"`
	Capabilities     []string                  `yaml:"capabilities"`
	Type             string                    `yaml:"type"`
	Command          string                    `yaml:"command"`
	Args             []string                  `yaml:"args"`
	Prompt           string                    `yaml:"prompt"`
	MaxChars         int                       `yaml:"max_chars"`
	MaxBytes         int                       `yaml:"max_bytes"`
	TimeoutSeconds   int                       `yaml:"timeout_seconds"`
	Language         string                    `yaml:"language"`
	ProviderOptions  map[string]map[string]any `yaml:"provider_options"`
	BaseURL          string                    `yaml:"base_url"`
	Headers          map[string]string         `yaml:"headers"`
	Profile          string                    `yaml:"profile"`
	PreferredProfile string                    `yaml:"preferred_profile"`
}

func (c MediaUnderstandingModelConfig) ResolvedType() MediaUnderstandingEntryType {
	t := strings.ToLower(strings.TrimSpace(c.Type))
	if t != "" {
		return MediaUnderstandingEntryType(t)
	}
	if strings.TrimSpace(c.Command) != "" {
		return MediaEntryTypeCLI
	}
	return MediaEntryTypeProvider
}

// MediaUnderstandingConfig defines defaults for media understanding of a capability.
type MediaUnderstandingConfig struct {
	Enabled         *bool                                `yaml:"enabled"`
	Scope           *MediaUnderstandingScopeConfig       `yaml:"scope"`
	MaxBytes        int                                  `yaml:"max_bytes"`
	MaxChars        int                                  `yaml:"max_chars"`
	Prompt          string                               `yaml:"prompt"`
	TimeoutSeconds  int                                  `yaml:"timeout_seconds"`
	Language        string                               `yaml:"language"`
	ProviderOptions map[string]map[string]any            `yaml:"provider_options"`
	BaseURL         string                               `yaml:"base_url"`
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

func (cfg *MediaToolsConfig) ConfigForCapability(capability MediaUnderstandingCapability) *MediaUnderstandingConfig {
	if cfg == nil {
		return nil
	}
	switch capability {
	case MediaCapabilityImage:
		return cfg.Image
	case MediaCapabilityAudio:
		return cfg.Audio
	case MediaCapabilityVideo:
		return cfg.Video
	default:
		return nil
	}
}

type SearchConfig struct {
	Provider string            `yaml:"provider"`
	Exa      ProviderExaConfig `yaml:"exa"`
}

type FetchConfig struct {
	Provider string               `yaml:"provider"`
	Exa      ProviderExaConfig    `yaml:"exa"`
	Direct   ProviderDirectConfig `yaml:"direct"`
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
// If UserMXID, BaseURL, and Token are set, users don't need to manually log in.
type BeeperConfig struct {
	UserMXID string `yaml:"user_mxid"` // Owning Matrix user for the built-in managed Beeper AI login
	BaseURL  string `yaml:"base_url"`  // Beeper AI proxy endpoint
	Token    string `yaml:"token"`     // Beeper Matrix access token
}

// ModelsConfig configures model catalog seeding.
type ModelsConfig struct {
	Mode      string                         `yaml:"mode"` // merge | replace
	Providers map[string]ModelProviderConfig `yaml:"providers"`
}

// ModelProviderConfig describes models for a specific provider.
type ModelProviderConfig struct {
	BaseURL string                  `yaml:"base_url"`
	APIKey  string                  `yaml:"api_key"`
	Headers map[string]string       `yaml:"headers"`
	Models  []ModelDefinitionConfig `yaml:"models"`
}

type ModelSelectionConfig struct {
	Primary   string   `yaml:"primary"`
	Fallbacks []string `yaml:"fallbacks"`
}

func (cfg *ModelsConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type rawModelsConfig ModelsConfig
	var raw rawModelsConfig
	if err := unmarshal(&raw); err != nil {
		return err
	}
	if len(raw.Providers) > 0 {
		normalizedProviders := make(map[string]ModelProviderConfig, len(raw.Providers))
		for key, provider := range raw.Providers {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if normalized == "" {
				return fmt.Errorf("models.providers contains an empty provider key")
			}
			if _, exists := normalizedProviders[normalized]; exists {
				return fmt.Errorf("models.providers contains duplicate provider key after normalization: %q", key)
			}
			normalizedProviders[normalized] = provider
		}
		raw.Providers = normalizedProviders
	}
	*cfg = ModelsConfig(raw)
	return nil
}

func (cfg *ModelsConfig) Provider(name string) ModelProviderConfig {
	if cfg == nil || len(cfg.Providers) == 0 {
		return ModelProviderConfig{}
	}
	return cfg.Providers[strings.ToLower(strings.TrimSpace(name))]
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

// BridgeConfig is an alias for the shared bridge config.
type BridgeConfig = bridgeconfig.BridgeConfig
