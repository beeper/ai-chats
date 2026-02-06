package connector

import (
	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/random"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/ai-bridge/pkg/opencodebridge"
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

// UserDefaults stores user-level default settings for new chats
type UserDefaults struct {
	Model           string   `json:"model,omitempty"`
	SystemPrompt    string   `json:"system_prompt,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
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
	Endpoint  string `json:"endpoint,omitempty"`
	AuthType  string `json:"auth_type,omitempty"` // bearer|apikey|none
	Token     string `json:"token,omitempty"`
	AuthURL   string `json:"auth_url,omitempty"` // Optional browser auth URL for manual token retrieval.
	Connected bool   `json:"connected,omitempty"`
	Kind      string `json:"kind,omitempty"` // nexus (default)
}

// UserLoginMetadata is stored on each login row to keep per-user settings.
type UserLoginMetadata struct {
	Persona              string         `json:"persona,omitempty"`
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
	ResponsePrefix       string         `json:"response_prefix,omitempty"`

	// FileAnnotationCache stores parsed PDF content from OpenRouter's file-parser plugin
	// Key is the file hash (SHA256), pruned after 7 days
	FileAnnotationCache map[string]FileAnnotation `json:"file_annotation_cache,omitempty"`

	// User-level defaults for new chats (set via provisioning API)
	Defaults *UserDefaults `json:"defaults,omitempty"`

	// Optional per-login tokens for external services
	ServiceTokens *ServiceTokens `json:"service_tokens,omitempty"`

	// AgentModelOverrides stores per-agent model overrides (agent ID -> model ID).
	AgentModelOverrides map[string]string `json:"agent_model_overrides,omitempty"`

	// Agent Builder room for managing agents
	BuilderRoomID networkid.PortalID `json:"builder_room_id,omitempty"`
	// Custom agents store (source of truth for user-created agents).
	CustomAgents map[string]*AgentDefinitionContent `json:"custom_agents,omitempty"`
	// Memory index fallback store for connectors without arbitrary Matrix state access.
	MemoryIndexes map[string][]MemoryIndexEntry `json:"memory_indexes,omitempty"`
	// Memory facts keyed by scope key then fact ID.
	MemoryFacts map[string]map[string]*MemoryFactContent `json:"memory_facts,omitempty"`
	// Last active room per agent (used for heartbeat delivery).
	LastActiveRoomByAgent map[string]string `json:"last_active_room_by_agent,omitempty"`
	// Heartbeat dedupe state per agent.
	HeartbeatState map[string]HeartbeatState `json:"heartbeat_state,omitempty"`

	// Global Memory room for shared agent memories
	GlobalMemoryRoomID networkid.PortalID `json:"global_memory_room_id,omitempty"`

	// OpenCode instances connected for this login (keyed by instance ID).
	OpenCodeInstances map[string]*opencodebridge.OpenCodeInstance `json:"opencode_instances,omitempty"`
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

// PortalMetadata stores per-room tuning knobs for the assistant.
type PortalMetadata struct {
	Model               string            `json:"model,omitempty"`                 // Set from room state
	SystemPrompt        string            `json:"system_prompt,omitempty"`         // Set from room state
	ResponsePrefix      string            `json:"response_prefix,omitempty"`       // Per-room response prefix override
	Temperature         float64           `json:"temperature,omitempty"`           // Set from room state
	MaxContextMessages  int               `json:"max_context_messages,omitempty"`  // Set from room state
	MaxCompletionTokens int               `json:"max_completion_tokens,omitempty"` // Set from room state
	ReasoningEffort     string            `json:"reasoning_effort,omitempty"`      // none, low, medium, high, xhigh
	Slug                string            `json:"slug,omitempty"`
	Title               string            `json:"title,omitempty"`
	TitleGenerated      bool              `json:"title_generated,omitempty"` // True if title was auto-generated
	WelcomeSent         bool              `json:"welcome_sent,omitempty"`
	AutoGreetingSent    bool              `json:"auto_greeting_sent,omitempty"`
	Capabilities        ModelCapabilities `json:"capabilities,omitempty"`
	LastRoomStateSync   int64             `json:"last_room_state_sync,omitempty"` // Track when we've synced room state
	PDFConfig           *PDFConfig        `json:"pdf_config,omitempty"`           // Per-room PDF processing configuration

	ConversationMode           string           `json:"conversation_mode,omitempty"`
	LastResponseID             string           `json:"last_response_id,omitempty"`
	EmitThinking               bool             `json:"emit_thinking,omitempty"`
	EmitToolArgs               bool             `json:"emit_tool_args,omitempty"`
	ThinkingLevel              string           `json:"thinking_level,omitempty"`   // off|minimal|low|medium|high|xhigh
	VerboseLevel               string           `json:"verbose_level,omitempty"`    // off|on|full
	ElevatedLevel              string           `json:"elevated_level,omitempty"`   // off|on|ask|full
	GroupActivation            string           `json:"group_activation,omitempty"` // mention|always
	GroupActivationNeedsIntro  bool             `json:"group_activation_needs_intro,omitempty"`
	GroupIntroSent             bool             `json:"group_intro_sent,omitempty"`
	SendPolicy                 string           `json:"send_policy,omitempty"` // allow|deny
	SessionResetAt             int64            `json:"session_reset_at,omitempty"`
	AbortedLastRun             bool             `json:"aborted_last_run,omitempty"`
	CompactionCount            int              `json:"compaction_count,omitempty"`
	MemoryFlushAt              int64            `json:"memory_flush_at,omitempty"`
	MemoryFlushCompactionCount int              `json:"memory_flush_compaction_count,omitempty"`
	MemoryBootstrapAt          int64            `json:"memory_bootstrap_at,omitempty"`
	SessionBootstrappedAt      int64            `json:"session_bootstrapped_at,omitempty"`
	SessionBootstrapByAgent    map[string]int64 `json:"session_bootstrap_by_agent,omitempty"`

	// Agent-related metadata
	DefaultAgentID       string `json:"default_agent_id,omitempty"`        // Agent assigned to this room (legacy name, same as AgentID)
	AgentID              string `json:"agent_id,omitempty"`                // Which agent is the ghost for this room
	AgentPrompt          string `json:"agent_prompt,omitempty"`            // Cached prompt for the assigned agent
	IsBuilderRoom        bool   `json:"is_builder_room,omitempty"`         // True if this is the Manage AI Chats room (protected from overrides)
	IsRawMode            bool   `json:"is_raw_mode,omitempty"`             // True if this is a playground/raw mode room (no directive processing)
	IsAgentDataRoom      bool   `json:"is_agent_data_room,omitempty"`      // True if this is a hidden room for storing agent data
	IsGlobalMemoryRoom   bool   `json:"is_global_memory_room,omitempty"`   // True if this is the global memory room
	IsCronRoom           bool   `json:"is_cron_room,omitempty"`            // True if this is a hidden cron room
	CronJobID            string `json:"cron_job_id,omitempty"`             // Cron job ID for cron rooms
	SubagentParentRoomID string `json:"subagent_parent_room_id,omitempty"` // Parent room ID for subagent sessions

	// OpenCode session metadata
	IsOpenCodeRoom       bool   `json:"is_opencode_room,omitempty"`
	OpenCodeInstanceID   string `json:"opencode_instance_id,omitempty"`
	OpenCodeSessionID    string `json:"opencode_session_id,omitempty"`
	OpenCodeReadOnly     bool   `json:"opencode_read_only,omitempty"`
	OpenCodeTitlePending bool   `json:"opencode_title_pending,omitempty"`

	// Ack reaction config - similar to OpenClaw's ack reactions
	AckReactionEmoji       string `json:"ack_reaction_emoji,omitempty"`        // Emoji to react with when message received (e.g., "👀", "🤔"). Empty = disabled.
	AckReactionRemoveAfter bool   `json:"ack_reaction_remove_after,omitempty"` // Remove the ack reaction after replying

	// Debounce configuration (0 = use default, -1 = disabled)
	DebounceMs int `json:"debounce_ms,omitempty"`

	// Per-session typing overrides (OpenClaw-style).
	TypingMode            string `json:"typing_mode,omitempty"`             // never|instant|thinking|message
	TypingIntervalSeconds *int   `json:"typing_interval_seconds,omitempty"` // Optional per-session override
}

func clonePortalMetadata(src *PortalMetadata) *PortalMetadata {
	if src == nil {
		return nil
	}

	clone := *src

	// Ensure OpenCode metadata is copied.
	clone.IsOpenCodeRoom = src.IsOpenCodeRoom
	clone.OpenCodeInstanceID = src.OpenCodeInstanceID
	clone.OpenCodeSessionID = src.OpenCodeSessionID
	clone.OpenCodeReadOnly = src.OpenCodeReadOnly
	clone.TypingMode = src.TypingMode
	clone.TypingIntervalSeconds = src.TypingIntervalSeconds

	if src.PDFConfig != nil {
		pdf := *src.PDFConfig
		clone.PDFConfig = &pdf
	}

	return &clone
}

// MessageMetadata keeps a tiny summary of each exchange so we can rebuild
// prompts using database history.
type MessageMetadata struct {
	Role             string `json:"role,omitempty"`
	Body             string `json:"body,omitempty"`
	CompletionID     string `json:"completion_id,omitempty"`
	FinishReason     string `json:"finish_reason,omitempty"`
	PromptTokens     int64  `json:"prompt_tokens,omitempty"`
	CompletionTokens int64  `json:"completion_tokens,omitempty"`
	Model            string `json:"model,omitempty"`
	ReasoningTokens  int64  `json:"reasoning_tokens,omitempty"`
	HasToolCalls     bool   `json:"has_tool_calls,omitempty"`
	Transcript       string `json:"transcript,omitempty"`

	// Media understanding (OpenClaw-style)
	MediaUnderstanding          []MediaUnderstandingOutput   `json:"media_understanding,omitempty"`
	MediaUnderstandingDecisions []MediaUnderstandingDecision `json:"media_understanding_decisions,omitempty"`

	// Turn tracking for the new schema
	TurnID  string `json:"turn_id,omitempty"`  // Unique identifier for this assistant turn
	AgentID string `json:"agent_id,omitempty"` // Which agent generated this (for multi-agent rooms)

	// Tool call tracking
	ToolCalls []ToolCallMetadata `json:"tool_calls,omitempty"` // List of tool calls in this turn

	// Canonical internal schema payload (AI SDK compatible).
	CanonicalSchema    string         `json:"canonical_schema,omitempty"`     // e.g. ai-sdk-ui-message-v1
	CanonicalUIMessage map[string]any `json:"canonical_ui_message,omitempty"` // AI SDK UIMessage-compatible payload

	// Timing information
	StartedAtMs    int64 `json:"started_at_ms,omitempty"`     // Unix ms when generation started
	FirstTokenAtMs int64 `json:"first_token_at_ms,omitempty"` // Unix ms of first token
	CompletedAtMs  int64 `json:"completed_at_ms,omitempty"`   // Unix ms when completed

	// Thinking/reasoning content (embedded, not separate)
	ThinkingContent    string `json:"thinking_content,omitempty"`     // Full thinking text
	ThinkingTokenCount int    `json:"thinking_token_count,omitempty"` // Number of thinking tokens

	// History exclusion
	ExcludeFromHistory bool `json:"exclude_from_history,omitempty"` // Exclude from LLM context (e.g., welcome messages)
}

// ToolCallMetadata tracks a tool call within a message
type ToolCallMetadata struct {
	CallID        string         `json:"call_id"`
	ToolName      string         `json:"tool_name"`
	ToolType      string         `json:"tool_type"` // builtin, provider, function, mcp
	Input         map[string]any `json:"input,omitempty"`
	Output        map[string]any `json:"output,omitempty"`
	Status        string         `json:"status"`                  // pending, running, completed, failed, timeout, cancelled
	ResultStatus  string         `json:"result_status,omitempty"` // success, error, partial
	ErrorMessage  string         `json:"error_message,omitempty"`
	StartedAtMs   int64          `json:"started_at_ms,omitempty"`
	CompletedAtMs int64          `json:"completed_at_ms,omitempty"`

	// Event IDs for timeline events (if emitted as separate events)
	CallEventID   string `json:"call_event_id,omitempty"`
	ResultEventID string `json:"result_event_id,omitempty"`
}

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
	if src.Role != "" {
		mm.Role = src.Role
	}
	if src.Body != "" {
		mm.Body = src.Body
	}
	if src.CompletionID != "" {
		mm.CompletionID = src.CompletionID
	}
	if src.FinishReason != "" {
		mm.FinishReason = src.FinishReason
	}
	if src.PromptTokens != 0 {
		mm.PromptTokens = src.PromptTokens
	}
	if src.CompletionTokens != 0 {
		mm.CompletionTokens = src.CompletionTokens
	}
	if src.Model != "" {
		mm.Model = src.Model
	}
	if src.ReasoningTokens != 0 {
		mm.ReasoningTokens = src.ReasoningTokens
	}
	if src.HasToolCalls {
		mm.HasToolCalls = true
	}

	// Copy new fields
	if src.TurnID != "" {
		mm.TurnID = src.TurnID
	}
	if src.AgentID != "" {
		mm.AgentID = src.AgentID
	}
	if len(src.ToolCalls) > 0 {
		mm.ToolCalls = src.ToolCalls
	}
	if src.CanonicalSchema != "" {
		mm.CanonicalSchema = src.CanonicalSchema
	}
	if len(src.CanonicalUIMessage) > 0 {
		mm.CanonicalUIMessage = src.CanonicalUIMessage
	}
	if src.StartedAtMs != 0 {
		mm.StartedAtMs = src.StartedAtMs
	}
	if src.FirstTokenAtMs != 0 {
		mm.FirstTokenAtMs = src.FirstTokenAtMs
	}
	if src.CompletedAtMs != 0 {
		mm.CompletedAtMs = src.CompletedAtMs
	}
	if src.ThinkingContent != "" {
		mm.ThinkingContent = src.ThinkingContent
	}
	if src.ThinkingTokenCount != 0 {
		mm.ThinkingTokenCount = src.ThinkingTokenCount
	}
}

var _ database.MetaMerger = (*MessageMetadata)(nil)

// NewTurnID generates a new unique turn ID
func NewTurnID() string {
	// Use a simple timestamp-based ID for now
	// Could be enhanced with UUID or other unique ID generation
	return "turn_" + generateShortID()
}

// NewCallID generates a new unique call ID for tool calls
func NewCallID() string {
	return "call_" + generateShortID()
}

// generateShortID generates a short unique ID (12 chars)
func generateShortID() string {
	return random.String(12)
}
