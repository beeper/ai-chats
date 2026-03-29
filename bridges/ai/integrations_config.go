package ai

import (
	_ "embed"
	"strings"
	"time"

	"go.mau.fi/util/ptr"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/agents/agentconfig"
	"github.com/beeper/agentremote/pkg/agents/toolpolicy"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/pkg/shared/bridgeconfig"
)

//go:embed integrations_example-config.yaml
var exampleNetworkConfig string

// Config represents the connector-specific configuration that is nested under
// the `network:` block in the main bridge config.
type Config struct {
	Beeper        BeeperConfig                       `yaml:"beeper"`
	Models        *ModelsConfig                      `yaml:"models"`
	Bridge        BridgeConfig                       `yaml:"bridge"`
	Tools         ToolProvidersConfig                `yaml:"tools"`
	ToolApprovals *ToolApprovalsRuntimeConfig        `yaml:"tool_approvals"`
	ToolPolicy    *toolpolicy.GlobalToolPolicyConfig `yaml:"tool_policy"`
	Agents        *AgentsConfig                      `yaml:"agents"`
	Channels      *ChannelsConfig                    `yaml:"channels"`
	Messages      *MessagesConfig                    `yaml:"messages"`
	Commands      *CommandsConfig                    `yaml:"commands"`
	Session       *SessionConfig                     `yaml:"session"`

	// Global settings
	DefaultSystemPrompt string        `yaml:"default_system_prompt"`
	ModelCacheDuration  time.Duration `yaml:"model_cache_duration"`

	// Inbound message processing configuration
	Inbound *InboundConfig `yaml:"inbound"`

	// Integration registration toggles.
	Integrations *IntegrationsConfig `yaml:"integrations"`

	// Module-level configs captured generically (e.g., cron:, memory:, memory_search:).
	Modules map[string]any `yaml:",inline"`
}

// IntegrationsConfig controls compile-time-available integration registration.
// Module names are keys in the Modules map.
// A bool value (true/false) enables/disables the module.
// A map value configures the module (with an optional "enabled" key).
// Absent modules default to enabled.
type IntegrationsConfig struct {
	Modules map[string]any `yaml:",inline"`
}

// ToolApprovalsRuntimeConfig controls runtime behaviour for tool approvals.
// This gates OpenAI MCP approvals (mcp_approval_request) and selected dangerous builtin tools.
type ToolApprovalsRuntimeConfig struct {
	Enabled         *bool    `yaml:"enabled"`
	TTLSeconds      int      `yaml:"ttl_seconds"`
	RequireForMCP   *bool    `yaml:"require_for_mcp"`
	RequireForTools []string `yaml:"require_for_tools"`
}

func (c *ToolApprovalsRuntimeConfig) WithDefaults() *ToolApprovalsRuntimeConfig {
	if c == nil {
		c = &ToolApprovalsRuntimeConfig{}
	}
	if c.Enabled == nil {
		c.Enabled = ptr.Ptr(true)
	}
	if c.TTLSeconds <= 0 {
		c.TTLSeconds = 600
	}
	if c.RequireForMCP == nil {
		c.RequireForMCP = ptr.Ptr(true)
	}
	if len(c.RequireForTools) == 0 {
		c.RequireForTools = []string{
			"message",
			"cron",
			"gravatar_set",

			// Boss/session mutation tools
			"create_agent",
			"fork_agent",
			"edit_agent",
			"delete_agent",
			"modify_room",
			"sessions_send",
			"sessions_spawn",
			"run_internal_command",
		}
	}
	return c
}

// AgentsConfig configures agent defaults.
type AgentsConfig struct {
	Defaults *AgentDefaultsConfig `yaml:"defaults"`
	List     []AgentEntryConfig   `yaml:"list"`
}

// AgentDefaultsConfig defines default agent settings.
type AgentDefaultsConfig struct {
	Subagents         *agentconfig.SubagentConfig `yaml:"subagents"`
	SkipBootstrap     bool                        `yaml:"skip_bootstrap"`
	BootstrapMaxChars int                         `yaml:"bootstrap_max_chars"`
	TimeoutSeconds    int                         `yaml:"timeout_seconds"`
	Model             *ModelSelectionConfig       `yaml:"model"`
	ImageModel        *ModelSelectionConfig       `yaml:"image_model"`
	ImageGeneration   *ModelSelectionConfig       `yaml:"image_generation_model"`
	PDFModel          *ModelSelectionConfig       `yaml:"pdf_model"`
	PDFEngine         string                      `yaml:"pdf_engine"`
	Compaction        *airuntime.PruningConfig    `yaml:"compaction"`
	SoulEvil          *agents.SoulEvilConfig      `yaml:"soul_evil"`
	Heartbeat         *HeartbeatConfig            `yaml:"heartbeat"`
	UserTimezone      string                      `yaml:"user_timezone"`
	EnvelopeTimezone  string                      `yaml:"envelope_timezone"`  // local|utc|user|IANA
	EnvelopeTimestamp string                      `yaml:"envelope_timestamp"` // on|off
	EnvelopeElapsed   string                      `yaml:"envelope_elapsed"`   // on|off
	TypingMode        string                      `yaml:"typing_mode"`        // never|instant|thinking|message
	TypingIntervalSec *int                        `yaml:"typing_interval_seconds"`
}

// AgentEntryConfig defines per-agent overrides.
type AgentEntryConfig struct {
	ID                string           `yaml:"id"`
	Heartbeat         *HeartbeatConfig `yaml:"heartbeat"`
	TypingMode        string           `yaml:"typing_mode"` // never|instant|thinking|message
	TypingIntervalSec *int             `yaml:"typing_interval_seconds"`
}

// HeartbeatConfig configures periodic heartbeat runs.
type HeartbeatConfig struct {
	Every            *string                     `yaml:"every"`
	ActiveHours      *HeartbeatActiveHoursConfig `yaml:"active_hours"`
	Model            *string                     `yaml:"model"`
	Session          *string                     `yaml:"session"`
	Target           *string                     `yaml:"target"`
	To               *string                     `yaml:"to"`
	Prompt           *string                     `yaml:"prompt"`
	AckMaxChars      *int                        `yaml:"ack_max_chars"`
	IncludeReasoning *bool                       `yaml:"include_reasoning"`
}

type HeartbeatActiveHoursConfig struct {
	Start    string `yaml:"start"`
	End      string `yaml:"end"`
	Timezone string `yaml:"timezone"`
}

// ChannelsConfig defines per-channel settings.
type ChannelsConfig struct {
	Defaults *ChannelDefaultsConfig `yaml:"defaults"`
	Matrix   *ChannelConfig         `yaml:"matrix"`
}

type ChannelDefaultsConfig struct {
	Heartbeat *ChannelHeartbeatVisibilityConfig `yaml:"heartbeat"`
}

type ChannelConfig struct {
	Heartbeat     *ChannelHeartbeatVisibilityConfig `yaml:"heartbeat"`
	ReplyToMode   string                            `yaml:"reply_to_mode"`  // off|first|all (Matrix)
	ThreadReplies string                            `yaml:"thread_replies"` // off|inbound|always (Matrix)
}

type ChannelHeartbeatVisibilityConfig struct {
	ShowOk       *bool `yaml:"show_ok"`
	ShowAlerts   *bool `yaml:"show_alerts"`
	UseIndicator *bool `yaml:"use_indicator"`
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

// SessionConfig configures session behavior.
type SessionConfig struct {
	Scope   string `yaml:"scope"`
	MainKey string `yaml:"main_key"`
}

// ToolProvidersConfig configures external tool providers like search and fetch.
type ToolProvidersConfig struct {
	Web   *WebToolsConfig    `yaml:"web"`
	Links *LinkPreviewConfig `yaml:"links"`
	Media *MediaToolsConfig  `yaml:"media"`
	MCP   *MCPToolsConfig    `yaml:"mcp"`
	VFS   *VFSToolsConfig    `yaml:"vfs"`
}

type WebToolsConfig struct {
	Search *SearchConfig `yaml:"search"`
	Fetch  *FetchConfig  `yaml:"fetch"`
}

// MCPToolsConfig configures generic MCP behavior.
type MCPToolsConfig struct {
	EnableStdio bool `yaml:"enable_stdio"`
}

// VFSToolsConfig configures virtual filesystem tools.
type VFSToolsConfig struct {
	ApplyPatch *ApplyPatchToolsConfig `yaml:"apply_patch"`
}

// ApplyPatchToolsConfig configures apply_patch availability.
type ApplyPatchToolsConfig struct {
	Enabled     *bool    `yaml:"enabled"`
	AllowModels []string `yaml:"allow_models"`
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
	Provider  string   `yaml:"provider"`
	Fallbacks []string `yaml:"fallbacks"`

	Exa ProviderExaConfig `yaml:"exa"`
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

// BeeperConfig contains Beeper Cloud proxy credentials for automatic login.
// If UserMXID, BaseURL, and Token are set, users don't need to manually log in.
type BeeperConfig struct {
	UserMXID string `yaml:"user_mxid"` // Owning Matrix user for the built-in managed Beeper Cloud login
	BaseURL  string `yaml:"base_url"`  // Beeper Cloud proxy endpoint
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
