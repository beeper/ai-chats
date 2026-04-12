package ai

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/random"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
	"github.com/beeper/agentremote/sdk"
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

// UserLoginMetadata is stored on each login row to keep per-user settings.
type UserLoginMetadata struct {
	Provider             string            `json:"provider,omitempty"` // Selected provider (beeper, openai, openrouter)
	Credentials          *LoginCredentials `json:"credentials,omitempty"`
	TitleGenerationModel string            `json:"title_generation_model,omitempty"` // Model to use for generating chat titles
	Agents               *bool             `json:"agents,omitempty"`                 // Nil/true enables agents, false limits login to model rooms
	ModelCache           *ModelCache       `json:"model_cache,omitempty"`
	Gravatar             *GravatarState    `json:"gravatar,omitempty"`
	Timezone             string            `json:"timezone,omitempty"`
	Profile              *UserProfile      `json:"profile,omitempty"`

	// FileAnnotationCache stores parsed PDF content from OpenRouter's file-parser plugin
	// Key is the file hash (SHA256), pruned after 7 days
	FileAnnotationCache map[string]FileAnnotation `json:"file_annotation_cache,omitempty"`

	// Custom agents store (source of truth for user-created agents).
	CustomAgents map[string]*AgentDefinitionContent `json:"custom_agents,omitempty"`

	// Provider health tracking
	ConsecutiveErrors int   `json:"consecutive_errors,omitempty"`
	LastErrorAt       int64 `json:"last_error_at,omitempty"` // Unix timestamp
}

func loginCredentials(meta *UserLoginMetadata) *LoginCredentials {
	if meta == nil {
		return nil
	}
	return meta.Credentials
}

func ensureLoginCredentials(meta *UserLoginMetadata) *LoginCredentials {
	if meta == nil {
		return nil
	}
	if meta.Credentials == nil {
		meta.Credentials = &LoginCredentials{}
	}
	return meta.Credentials
}

func loginCredentialAPIKey(meta *UserLoginMetadata) string {
	if creds := loginCredentials(meta); creds != nil {
		return strings.TrimSpace(creds.APIKey)
	}
	return ""
}

func loginCredentialBaseURL(meta *UserLoginMetadata) string {
	if creds := loginCredentials(meta); creds != nil {
		return strings.TrimSpace(creds.BaseURL)
	}
	return ""
}

func loginCredentialServiceTokens(meta *UserLoginMetadata) *ServiceTokens {
	if creds := loginCredentials(meta); creds != nil {
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

// PortalMetadata stores runtime-only per-room state. Persistent room state is mirrored
// into AI-owned database tables and is not serialized through bridgev2 metadata.
type PortalMetadata struct {
	AckReactionEmoji       string     `json:"-"`
	AckReactionRemoveAfter bool       `json:"-"`
	PDFConfig              *PDFConfig `json:"-"`

	Slug             string `json:"-"`
	Title            string `json:"-"`
	TitleGenerated   bool   `json:"-"`
	WelcomeSent      bool   `json:"-"`
	AutoGreetingSent bool   `json:"-"`

	SessionResetAt          int64            `json:"-"`
	AbortedLastRun          bool             `json:"-"`
	CompactionCount         int              `json:"-"`
	SessionBootstrappedAt   int64            `json:"-"`
	SessionBootstrapByAgent map[string]int64 `json:"-"`

	ModuleMeta           map[string]any `json:"-"` // Generic per-module metadata (e.g., cron room markers, memory flush state)
	SubagentParentRoomID string         `json:"-"` // Parent room ID for subagent sessions

	// Runtime-only overrides (not persisted)
	DisabledTools        []string        `json:"-"`
	ResolvedTarget       *ResolvedTarget `json:"-"`
	RuntimeModelOverride string          `json:"-"`
	RuntimeReasoning     string          `json:"-"`

	// Debounce configuration (0 = use default, -1 = disabled)
	DebounceMs int `json:"-"`

	// Per-session typing overrides (OpenClaw-style).
	TypingMode            string `json:"-"` // never|instant|thinking|message
	TypingIntervalSeconds *int   `json:"-"`
	portalStateLoaded     bool   `json:"-"`
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

func (m *PortalMetadata) ModuleMetaValue(key string) any {
	if m == nil || m.ModuleMeta == nil {
		return nil
	}
	return m.ModuleMeta[key]
}

func (m *PortalMetadata) SetModuleMetaValue(key string, value any) {
	m.SetModuleMeta(key, value)
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
	return isModuleInternalRoom(m)
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

func agentsEnabled(meta *UserLoginMetadata) bool {
	if meta == nil || meta.Agents == nil {
		return false
	}
	return *meta.Agents
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
