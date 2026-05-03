package ai

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/random"
	"maunium.net/go/mautrix/bridgev2/database"

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
	OpenAI     string `json:"openai,omitempty"`
	OpenRouter string `json:"openrouter,omitempty"`
	Exa        string `json:"exa,omitempty"`
}

// UserLoginMetadata is the durable bridgev2-owned login metadata surface.
type UserLoginMetadata struct {
	Provider             string            `json:"provider,omitempty"` // Selected provider (openai, openrouter, magic_proxy)
	Credentials          *LoginCredentials `json:"credentials,omitempty"`
	TitleGenerationModel string            `json:"title_generation_model,omitempty"`
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
	return &clone
}

func serviceTokensEmpty(tokens *ServiceTokens) bool {
	if tokens == nil {
		return true
	}
	return strings.TrimSpace(tokens.OpenAI) == "" &&
		strings.TrimSpace(tokens.OpenRouter) == "" &&
		strings.TrimSpace(tokens.Exa) == ""
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
	DisclaimerSent   bool   `json:"disclaimer_sent,omitempty"`
	AutoGreetingSent bool   `json:"auto_greeting_sent,omitempty"`

	AbortedLastRun          bool             `json:"aborted_last_run,omitempty"`
	SessionBootstrapByAgent map[string]int64 `json:"session_bootstrap_by_agent,omitempty"`
	InternalRoomKind        string           `json:"internal_room_kind,omitempty"`

	// Runtime-only overrides (not persisted)
	DisabledTools        []string        `json:"-"`
	ResolvedTarget       *ResolvedTarget `json:"-"`
	RuntimeModelOverride string          `json:"-"`
	RuntimeReasoning     string          `json:"-"`

	// Debounce configuration (0 = use default, -1 = disabled)
	DebounceMs int `json:"debounce_ms,omitempty"`

	// Per-session typing overrides (AgentRemote-style).
	TypingMode            string `json:"typing_mode,omitempty"` // never|instant|thinking|message
	TypingIntervalSeconds *int   `json:"typing_interval_seconds,omitempty"`
}

func (m *PortalMetadata) AgentID() string {
	return resolveAgentID(m)
}

func (m *PortalMetadata) CompactionCounter() int {
	return 0
}

func (m *PortalMetadata) InternalRoom() bool {
	return m != nil && strings.TrimSpace(m.InternalRoomKind) != ""
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

	// Media understanding (AgentRemote-style)
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
