package ai

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/random"
	"maunium.net/go/mautrix/bridgev2/database"

	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
	"github.com/beeper/agentremote/sdk"
)

// ModelCache stores available models cached in AI-owned login runtime state.
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

// LoginCredentials stores the per-login credentials and service-specific tokens.
type LoginCredentials struct {
	APIKey        string         `json:"api_key,omitempty"`
	BaseURL       string         `json:"base_url,omitempty"`
	ServiceTokens *ServiceTokens `json:"service_tokens,omitempty"`
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

// UserLoginMetadata is the durable bridgev2-owned login metadata surface.
type UserLoginMetadata struct {
	Provider             string            `json:"provider,omitempty"` // Selected provider (openai, openrouter, magic_proxy)
	Credentials          *LoginCredentials `json:"credentials,omitempty"`
	TitleGenerationModel string            `json:"title_generation_model,omitempty"`
	Agents               *bool             `json:"agents,omitempty"`
	Timezone             string            `json:"timezone,omitempty"`
	Profile              *UserProfile      `json:"profile,omitempty"`
	Gravatar             *GravatarState    `json:"gravatar,omitempty"`
}

func loginCredentials(cfg *aiLoginConfig) *LoginCredentials {
	if cfg == nil {
		return nil
	}
	return cfg.Credentials
}

func ensureLoginCredentials(cfg *aiLoginConfig) *LoginCredentials {
	if cfg == nil {
		return nil
	}
	if cfg.Credentials == nil {
		cfg.Credentials = &LoginCredentials{}
	}
	return cfg.Credentials
}

func loginCredentialAPIKey(cfg *aiLoginConfig) string {
	if creds := loginCredentials(cfg); creds != nil {
		return strings.TrimSpace(creds.APIKey)
	}
	return ""
}

func loginCredentialBaseURL(cfg *aiLoginConfig) string {
	if creds := loginCredentials(cfg); creds != nil {
		return strings.TrimSpace(creds.BaseURL)
	}
	return ""
}

func loginCredentialServiceTokens(cfg *aiLoginConfig) *ServiceTokens {
	if creds := loginCredentials(cfg); creds != nil {
		return creds.ServiceTokens
	}
	return nil
}

func loginCredentialsEmpty(creds *LoginCredentials) bool {
	if creds == nil {
		return true
	}
	return strings.TrimSpace(creds.APIKey) == "" &&
		strings.TrimSpace(creds.BaseURL) == "" &&
		serviceTokensEmpty(creds.ServiceTokens)
}

func cloneServiceTokens(src *ServiceTokens) *ServiceTokens {
	if src == nil {
		return nil
	}
	clone := *src
	if src.DesktopAPIInstances != nil {
		clone.DesktopAPIInstances = maps.Clone(src.DesktopAPIInstances)
	}
	if src.MCPServers != nil {
		clone.MCPServers = maps.Clone(src.MCPServers)
	}
	return &clone
}

func serviceTokensEmpty(tokens *ServiceTokens) bool {
	if tokens == nil {
		return true
	}
	if len(tokens.DesktopAPIInstances) > 0 {
		for _, instance := range tokens.DesktopAPIInstances {
			if strings.TrimSpace(instance.Token) != "" || strings.TrimSpace(instance.BaseURL) != "" {
				return false
			}
		}
	}
	if len(tokens.MCPServers) > 0 {
		for _, server := range tokens.MCPServers {
			if strings.TrimSpace(server.Transport) != "" ||
				strings.TrimSpace(server.Endpoint) != "" ||
				strings.TrimSpace(server.Command) != "" ||
				len(server.Args) > 0 ||
				strings.TrimSpace(server.Token) != "" ||
				strings.TrimSpace(server.AuthURL) != "" ||
				strings.TrimSpace(server.AuthType) != "" ||
				strings.TrimSpace(server.Kind) != "" ||
				server.Connected {
				return false
			}
		}
	}
	return strings.TrimSpace(tokens.OpenAI) == "" &&
		strings.TrimSpace(tokens.OpenRouter) == "" &&
		strings.TrimSpace(tokens.Exa) == "" &&
		strings.TrimSpace(tokens.Brave) == "" &&
		strings.TrimSpace(tokens.Perplexity) == "" &&
		strings.TrimSpace(tokens.DesktopAPI) == ""
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

// PortalMetadata stores durable room configuration/state plus transient runtime overrides.
type PortalMetadata struct {
	AckReactionEmoji       string     `json:"ack_reaction_emoji,omitempty"`
	AckReactionRemoveAfter bool       `json:"ack_reaction_remove_after,omitempty"`
	PDFConfig              *PDFConfig `json:"pdf_config,omitempty"`

	Slug             string `json:"slug,omitempty"`
	TitleGenerated   bool   `json:"title_generated,omitempty"`
	WelcomeSent      bool   `json:"welcome_sent,omitempty"`
	AutoGreetingSent bool   `json:"auto_greeting_sent,omitempty"`

	SessionResetAt                 int64                           `json:"session_reset_at,omitempty"`
	AbortedLastRun                 bool                            `json:"aborted_last_run,omitempty"`
	CompactionCount                int                             `json:"compaction_count,omitempty"`
	SessionBootstrapByAgent        map[string]int64                `json:"session_bootstrap_by_agent,omitempty"`
	InternalRoomKind               string                          `json:"internal_room_kind,omitempty"` // e.g. cron, heartbeat
	CompactionLastPromptTokens     int64                           `json:"compaction_last_prompt_tokens,omitempty"`
	CompactionLastCompletionTokens int64                           `json:"compaction_last_completion_tokens,omitempty"`
	MemoryModuleState              *integrationruntime.MemoryState `json:"memory_state,omitempty"`

	SubagentParentRoomID string `json:"subagent_parent_room_id,omitempty"` // Parent room ID for subagent sessions

	// Runtime-only overrides (not persisted)
	DisabledTools        []string        `json:"-"`
	ResolvedTarget       *ResolvedTarget `json:"-"`
	RuntimeModelOverride string          `json:"-"`
	RuntimeReasoning     string          `json:"-"`

	// Debounce configuration (0 = use default, -1 = disabled)
	DebounceMs int `json:"debounce_ms,omitempty"`

	// Per-session typing overrides (OpenClaw-style).
	TypingMode            string `json:"typing_mode,omitempty"` // never|instant|thinking|message
	TypingIntervalSeconds *int   `json:"typing_interval_seconds,omitempty"`
}

func (m *PortalMetadata) AgentID() string {
	return resolveAgentID(m)
}

func (m *PortalMetadata) CompactionCounter() int {
	if m == nil {
		return 0
	}
	return m.CompactionCount
}

func (m *PortalMetadata) InternalRoom() bool {
	return m != nil && strings.TrimSpace(m.InternalRoomKind) != ""
}

func (m *PortalMetadata) MemoryState() *integrationruntime.MemoryState {
	if m == nil {
		return nil
	}
	return m.MemoryModuleState
}

func (m *PortalMetadata) EnsureMemoryState() *integrationruntime.MemoryState {
	if m == nil {
		return nil
	}
	if m.MemoryModuleState == nil {
		m.MemoryModuleState = &integrationruntime.MemoryState{}
	}
	return m.MemoryModuleState
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

func clonePortalMetadata(src *PortalMetadata) *PortalMetadata {
	if src == nil {
		return nil
	}

	clone := *src

	if src.PDFConfig != nil {
		pdf := *src.PDFConfig
		clone.PDFConfig = &pdf
	}
	if src.TypingIntervalSeconds != nil {
		interval := *src.TypingIntervalSeconds
		clone.TypingIntervalSeconds = &interval
	}

	if src.SessionBootstrapByAgent != nil {
		clone.SessionBootstrapByAgent = maps.Clone(src.SessionBootstrapByAgent)
	}

	if len(src.DisabledTools) > 0 {
		clone.DisabledTools = slices.Clone(src.DisabledTools)
	}
	if src.MemoryModuleState != nil {
		memoryState := *src.MemoryModuleState
		clone.MemoryModuleState = &memoryState
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
	sdk.BaseMessageMetadata
	sdk.AssistantMessageMetadata

	// Media understanding (OpenClaw-style)
	MediaUnderstanding          []MediaUnderstandingOutput   `json:"media_understanding,omitempty"`
	MediaUnderstandingDecisions []MediaUnderstandingDecision `json:"media_understanding_decisions,omitempty"`

	// Multimodal history: media attached to this message for re-injection into prompts.
	MediaURL string `json:"media_url,omitempty"` // mxc:// URL for user-sent media (image, PDF, audio, video)
	MimeType string `json:"mime_type,omitempty"` // MIME type of user-sent media
}

type GeneratedFileRef = sdk.GeneratedFileRef

type ToolCallMetadata = sdk.ToolCallMetadata

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
	sdk.CopyFromBaseAndAssistant(&mm.BaseMessageMetadata, &src.BaseMessageMetadata, &mm.AssistantMessageMetadata, &src.AssistantMessageMetadata)
}

var _ database.MetaMerger = (*MessageMetadata)(nil)

// NewCallID generates a new unique call ID for tool calls
func NewCallID() string {
	return "call_" + random.String(12)
}
