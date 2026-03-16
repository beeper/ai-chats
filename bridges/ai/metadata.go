package ai

import (
	"encoding/json"
	"maps"
	"slices"

	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/random"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

// ModelCache stores available models (cached in UserLoginMetadata)
// Uses provider-agnostic ModelInfo instead of openai.Model
type ModelCache struct {
	Models        []ModelInfo `json:"models,omitempty"`
	LastRefresh   int64       `json:"last_refresh,omitempty"`
	CacheDuration int64       `json:"cache_duration,omitempty"` // seconds
}

// ModelCapabilities stores computed capabilities for a model
// This is NOT sent to the API, just used for local caching
type ModelCapabilities struct {
	SupportsVision      bool `json:"supports_vision"`
	SupportsReasoning   bool `json:"supports_reasoning"` // Models that support reasoning_effort parameter
	SupportsPDF         bool `json:"supports_pdf"`
	SupportsImageGen    bool `json:"supports_image_gen"`
	SupportsAudio       bool `json:"supports_audio"`        // Models that accept audio input
	SupportsVideo       bool `json:"supports_video"`        // Models that accept video input
	SupportsToolCalling bool `json:"supports_tool_calling"` // Models that support function calling
}

// PDFConfig stores per-room PDF processing configuration
type PDFConfig struct {
	Engine string `json:"engine,omitempty"` // pdf-text (free), mistral-ocr (OCR, paid, default), native
}

// FileAnnotation stores cached parsed PDF content from OpenRouter's file-parser plugin
type FileAnnotation struct {
	FileHash   string `json:"file_hash"`            // SHA256 hash of the file content
	ParsedText string `json:"parsed_text"`          // Extracted text content
	PageCount  int    `json:"page_count,omitempty"` // Number of pages
	CreatedAt  int64  `json:"created_at"`           // Unix timestamp when cached
}

type UserProfile struct {
	Name               string `json:"name,omitempty"`
	Occupation         string `json:"occupation,omitempty"`
	AboutUser          string `json:"about_user,omitempty"`
	CustomInstructions string `json:"custom_instructions,omitempty"`
}

// ServiceTokens stores optional per-login credentials for external services.
type ServiceTokens struct {
	OpenAI              string                        `json:"openai,omitempty"`
	OpenRouter          string                        `json:"openrouter,omitempty"`
	Exa                 string                        `json:"exa,omitempty"`
	Brave               string                        `json:"brave,omitempty"`
	Perplexity          string                        `json:"perplexity,omitempty"`
	DesktopAPI          string                        `json:"desktop_api,omitempty"`
	DesktopAPIInstances map[string]DesktopAPIInstance `json:"desktop_api_instances,omitempty"`
	MCPServers          map[string]MCPServerConfig    `json:"mcp_servers,omitempty"`
}

type DesktopAPIInstance struct {
	Token   string `json:"token,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
}

// MCPServerConfig stores one MCP server connection for a login.
// The map key in ServiceTokens.MCPServers is the server name.
type MCPServerConfig struct {
	Transport string   `json:"transport,omitempty"` // streamable_http|stdio
	Endpoint  string   `json:"endpoint,omitempty"`  // streamable HTTP endpoint
	Command   string   `json:"command,omitempty"`   // stdio command path/binary
	Args      []string `json:"args,omitempty"`      // stdio command args
	AuthType  string   `json:"auth_type,omitempty"` // bearer|apikey|none
	Token     string   `json:"token,omitempty"`
	AuthURL   string   `json:"auth_url,omitempty"` // Optional browser auth URL for manual token retrieval.
	Connected bool     `json:"connected,omitempty"`
	Kind      string   `json:"kind,omitempty"` // generic
}

// ToolApprovalsConfig stores per-login persisted tool approval rules.
// This is used by the tool approval system to support "always allow" decisions.
type ToolApprovalsConfig struct {
	// MCPAlwaysAllow contains exact-match allow rules for MCP approvals.
	// Matching is done on normalized (trim + lowercase) server label + tool name.
	MCPAlwaysAllow []MCPAlwaysAllowRule `json:"mcp_always_allow,omitempty"`

	// BuiltinAlwaysAllow contains exact-match allow rules for builtin tool approvals.
	// Matching is done on normalized (trim + lowercase) tool name + action.
	// Action "" means "any action".
	BuiltinAlwaysAllow []BuiltinAlwaysAllowRule `json:"builtin_always_allow,omitempty"`
}

type MCPAlwaysAllowRule struct {
	ServerLabel string `json:"server_label,omitempty"`
	ToolName    string `json:"tool_name,omitempty"`
}

type BuiltinAlwaysAllowRule struct {
	ToolName string `json:"tool_name,omitempty"`
	Action   string `json:"action,omitempty"`
}

// UserLoginMetadata is stored on each login row to keep per-user settings.
type UserLoginMetadata struct {
	Provider             string         `json:"provider,omitempty"` // Selected provider (beeper, openai, openrouter)
	APIKey               string         `json:"api_key,omitempty"`
	BaseURL              string         `json:"base_url,omitempty"`               // Per-user API endpoint
	TitleGenerationModel string         `json:"title_generation_model,omitempty"` // Model to use for generating chat titles
	NextChatIndex        int            `json:"next_chat_index,omitempty"`
	DefaultChatPortalID  string         `json:"default_chat_portal_id,omitempty"`
	ModelCache           *ModelCache    `json:"model_cache,omitempty"`
	ChatsSynced          bool           `json:"chats_synced,omitempty"` // True after initial bootstrap completed successfully
	Gravatar             *GravatarState `json:"gravatar,omitempty"`
	Timezone             string         `json:"timezone,omitempty"`
	Profile              *UserProfile   `json:"profile,omitempty"`

	// FileAnnotationCache stores parsed PDF content from OpenRouter's file-parser plugin
	// Key is the file hash (SHA256), pruned after 7 days
	FileAnnotationCache map[string]FileAnnotation `json:"file_annotation_cache,omitempty"`

	// Optional per-login tokens for external services
	ServiceTokens *ServiceTokens `json:"service_tokens,omitempty"`

	// Tool approval rules (e.g. "always allow" decisions for MCP approvals or dangerous builtin tools).
	ToolApprovals *ToolApprovalsConfig `json:"tool_approvals,omitempty"`

	// Custom agents store (source of truth for user-created agents).
	CustomAgents map[string]*AgentDefinitionContent `json:"custom_agents,omitempty"`
	// Last active room per agent (used for heartbeat delivery).
	LastActiveRoomByAgent map[string]string `json:"last_active_room_by_agent,omitempty"`
	// Heartbeat dedupe state per agent.
	HeartbeatState map[string]HeartbeatState `json:"heartbeat_state,omitempty"`
	// LastHeartbeatEvent is the last emitted heartbeat event for this login (command-only debug surface).
	LastHeartbeatEvent *HeartbeatEventPayload `json:"last_heartbeat_event,omitempty"`

	// Provider health tracking
	ConsecutiveErrors int   `json:"consecutive_errors,omitempty"`
	LastErrorAt       int64 `json:"last_error_at,omitempty"` // Unix timestamp
}

// HeartbeatState tracks last heartbeat delivery for dedupe.
type HeartbeatState struct {
	LastHeartbeatText   string `json:"last_heartbeat_text,omitempty"`
	LastHeartbeatSentAt int64  `json:"last_heartbeat_sent_at,omitempty"`
}

// GravatarProfile stores the selected Gravatar profile for a login.
type GravatarProfile struct {
	Email     string         `json:"email,omitempty"`
	Hash      string         `json:"hash,omitempty"`
	Profile   map[string]any `json:"profile,omitempty"` // Full profile payload
	FetchedAt int64          `json:"fetched_at,omitempty"`
}

// GravatarState stores Gravatar profile state for a login.
type GravatarState struct {
	Primary *GravatarProfile `json:"primary,omitempty"`
}

// PortalMetadata stores non-derivable per-room runtime state.
type PortalMetadata struct {
	AckReactionEmoji       string     `json:"ack_reaction_emoji,omitempty"`
	AckReactionRemoveAfter bool       `json:"ack_reaction_remove_after,omitempty"`
	PDFConfig              *PDFConfig `json:"pdf_config,omitempty"`

	Slug             string `json:"slug,omitempty"`
	Title            string `json:"title,omitempty"`
	TitleGenerated   bool   `json:"title_generated,omitempty"` // True if title was auto-generated
	WelcomeSent      bool   `json:"welcome_sent,omitempty"`
	AutoGreetingSent bool   `json:"auto_greeting_sent,omitempty"`

	SessionResetAt          int64            `json:"session_reset_at,omitempty"`
	AbortedLastRun          bool             `json:"aborted_last_run,omitempty"`
	CompactionCount         int              `json:"compaction_count,omitempty"`
	SessionBootstrappedAt   int64            `json:"session_bootstrapped_at,omitempty"`
	SessionBootstrapByAgent map[string]int64 `json:"session_bootstrap_by_agent,omitempty"`

	ModuleMeta           map[string]any `json:"module_meta,omitempty"`             // Generic per-module metadata (e.g., cron room markers, memory flush state)
	SubagentParentRoomID string         `json:"subagent_parent_room_id,omitempty"` // Parent room ID for subagent sessions

	// Runtime-only overrides (not persisted)
	DisabledTools        []string        `json:"-"`
	ResolvedTarget       *ResolvedTarget `json:"-"`
	RuntimeModelOverride string          `json:"-"`
	RuntimeReasoning     string          `json:"-"`

	// Debounce configuration (0 = use default, -1 = disabled)
	DebounceMs int `json:"debounce_ms,omitempty"`

	// Per-session typing overrides (OpenClaw-style).
	TypingMode            string `json:"typing_mode,omitempty"`             // never|instant|thinking|message
	TypingIntervalSeconds *int   `json:"typing_interval_seconds,omitempty"` // Optional per-session override

}

// SetModuleMeta sets a key in the ModuleMeta map, initializing the map if necessary.
func (m *PortalMetadata) SetModuleMeta(key string, value any) {
	if m == nil {
		return
	}
	if m.ModuleMeta == nil {
		m.ModuleMeta = make(map[string]any)
	}
	m.ModuleMeta[key] = value
}

func cloneUserLoginMetadata(src *UserLoginMetadata) (*UserLoginMetadata, error) {
	if src == nil {
		return &UserLoginMetadata{}, nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return nil, err
	}
	var clone UserLoginMetadata
	if err = json.Unmarshal(data, &clone); err != nil {
		return nil, err
	}
	return &clone, nil
}

func mergeServiceTokens(existing, incoming *ServiceTokens) *ServiceTokens {
	if incoming == nil {
		return existing
	}
	if existing == nil {
		clone := *incoming
		if incoming.DesktopAPIInstances != nil {
			clone.DesktopAPIInstances = maps.Clone(incoming.DesktopAPIInstances)
		}
		if incoming.MCPServers != nil {
			clone.MCPServers = maps.Clone(incoming.MCPServers)
		}
		return &clone
	}

	merged := *existing
	if incoming.OpenAI != "" {
		merged.OpenAI = incoming.OpenAI
	}
	if incoming.OpenRouter != "" {
		merged.OpenRouter = incoming.OpenRouter
	}
	if incoming.Exa != "" {
		merged.Exa = incoming.Exa
	}
	if incoming.Brave != "" {
		merged.Brave = incoming.Brave
	}
	if incoming.Perplexity != "" {
		merged.Perplexity = incoming.Perplexity
	}
	if incoming.DesktopAPI != "" {
		merged.DesktopAPI = incoming.DesktopAPI
	}
	if len(incoming.DesktopAPIInstances) > 0 {
		if merged.DesktopAPIInstances == nil {
			merged.DesktopAPIInstances = make(map[string]DesktopAPIInstance, len(incoming.DesktopAPIInstances))
		}
		for key, value := range incoming.DesktopAPIInstances {
			merged.DesktopAPIInstances[key] = value
		}
	}
	if len(incoming.MCPServers) > 0 {
		if merged.MCPServers == nil {
			merged.MCPServers = make(map[string]MCPServerConfig, len(incoming.MCPServers))
		}
		for key, value := range incoming.MCPServers {
			merged.MCPServers[key] = value
		}
	}
	return &merged
}

func isSimpleMode(meta *PortalMetadata) bool {
	return meta != nil && meta.ResolvedTarget != nil && meta.ResolvedTarget.Kind == ResolvedTargetModel
}

func clonePortalMetadata(src *PortalMetadata) *PortalMetadata {
	if src == nil {
		return nil
	}

	clone := *src

	if src.PDFConfig != nil {
		pdf := *src.PDFConfig
		clone.PDFConfig = &pdf
	}

	if src.SessionBootstrapByAgent != nil {
		clone.SessionBootstrapByAgent = maps.Clone(src.SessionBootstrapByAgent)
	}

	if len(src.DisabledTools) > 0 {
		clone.DisabledTools = slices.Clone(src.DisabledTools)
	}

	if src.ModuleMeta != nil {
		clone.ModuleMeta = make(map[string]any, len(src.ModuleMeta))
		for k, v := range src.ModuleMeta {
			clone.ModuleMeta[k] = jsonutil.DeepCloneAny(v)
		}
	}
	if src.ResolvedTarget != nil {
		target := *src.ResolvedTarget
		clone.ResolvedTarget = &target
	}

	return &clone
}

// MessageMetadata keeps a tiny summary of each exchange so we can rebuild
// prompts using database history.
type MessageMetadata struct {
	agentremote.BaseMessageMetadata
	agentremote.AssistantMessageMetadata

	// Media understanding (OpenClaw-style)
	MediaUnderstanding          []MediaUnderstandingOutput   `json:"media_understanding,omitempty"`
	MediaUnderstandingDecisions []MediaUnderstandingDecision `json:"media_understanding_decisions,omitempty"`

	// Multimodal history: media attached to this message for re-injection into prompts.
	MediaURL string `json:"media_url,omitempty"` // mxc:// URL for user-sent media (image, PDF, audio, video)
	MimeType string `json:"mime_type,omitempty"` // MIME type of user-sent media
}

type GeneratedFileRef = agentremote.GeneratedFileRef

type ToolCallMetadata = agentremote.ToolCallMetadata

// GhostMetadata stores metadata for AI model ghosts
type GhostMetadata struct {
	LastSync jsontime.Unix `json:"last_sync,omitempty"`
}

// CopyFrom allows the metadata struct to participate in mautrix's meta merge.
func (mm *MessageMetadata) CopyFrom(other any) {
	src, ok := other.(*MessageMetadata)
	if !ok || src == nil {
		return
	}
	mm.CopyFromBase(&src.BaseMessageMetadata)
	mm.CopyFromAssistant(&src.AssistantMessageMetadata)
}

var _ database.MetaMerger = (*MessageMetadata)(nil)

// NewCallID generates a new unique call ID for tool calls
func NewCallID() string {
	return "call_" + random.String(12)
}

func isModuleInternalRoom(meta *PortalMetadata) bool {
	return moduleRoomKind(meta) != ""
}

func moduleRoomKind(meta *PortalMetadata) string {
	if meta == nil || meta.ModuleMeta == nil {
		return ""
	}
	for name, v := range meta.ModuleMeta {
		if m, ok := v.(map[string]any); ok {
			if internal, _ := m["is_internal_room"].(bool); internal {
				return name
			}
		}
	}
	return ""
}
