package runtime

import (
	"context"
	"time"

	"github.com/openai/openai-go/v3"
)

// Optional Host capability interfaces.
// Modules type-assert Host to these for additional runtime support.
// The connector implements them on the same struct that implements Host.

// RawLoggerAccess provides access to the underlying logger (e.g. zerolog.Logger).
type RawLoggerAccess interface {
	RawLogger() any
}

// PortalManager provides portal lifecycle operations.
type PortalManager interface {
	GetOrCreatePortal(ctx context.Context, portalID string, receiver string, displayName string, setupMeta func(meta any)) (portal any, roomID string, err error)
	SavePortal(ctx context.Context, portal any, reason string) error
	PortalRoomID(portal any) string
	PortalKeyString(portal any) string
}

// MetadataAccess provides generic read/write access to portal metadata.
type MetadataAccess interface {
	GetModuleMeta(meta any, key string) any
	SetModuleMeta(meta any, key string, value any)
	IsRawMode(meta any) bool
	AgentIDFromMeta(meta any) string
	CompactionCount(meta any) int
	IsGroupChat(ctx context.Context, portal any) bool
	IsInternalRoom(meta any) bool
	// PortalMeta extracts the metadata object from a portal.
	PortalMeta(portal any) any
	// CloneMeta returns a shallow copy of the portal's metadata.
	CloneMeta(portal any) any
	// SetMetaField sets a named field on a metadata object.
	SetMetaField(meta any, key string, value any)
}

// MessageSummary is a generic message summary.
type MessageSummary struct {
	Role string
	Body string
}

// AssistantMessageInfo is a generic assistant response.
type AssistantMessageInfo struct {
	Body             string
	Model            string
	PromptTokens     int64
	CompletionTokens int64
}

// MessageHelper provides message read/write operations.
type MessageHelper interface {
	RecentMessages(ctx context.Context, portal any, count int) []MessageSummary
	LastAssistantMessage(ctx context.Context, portal any) (id string, timestamp int64)
	WaitForAssistantMessage(ctx context.Context, portal any, afterID string, afterTS int64) (*AssistantMessageInfo, bool)
}

// HeartbeatHelper provides extended heartbeat capabilities beyond basic Heartbeat.
type HeartbeatHelper interface {
	RunHeartbeatOnce(ctx context.Context, reason string) (status string, reasonMsg string)
	ResolveHeartbeatSessionPortal(agentID string) (portal any, sessionKey string, err error)
	ResolveHeartbeatSessionKey(agentID string) string
	HeartbeatAckMaxChars(agentID string) int
	EnqueueSystemEvent(sessionKey string, text string, agentID string)
	PersistSystemEvents()
	// ResolveLastTarget returns the last delivery channel/target for heartbeat sessions.
	ResolveLastTarget(agentID string) (channel string, target string, ok bool)
}

// AgentHelper provides agent configuration access.
type AgentHelper interface {
	ResolveAgentID(raw string, fallbackDefault string) string
	NormalizeAgentID(raw string) string
	AgentExists(normalizedID string) bool
	DefaultAgentID() string
	AgentTimeoutSeconds() int
	UserTimezone() (tz string, loc *time.Location)
	// NormalizeThinkingLevel normalizes a thinking level string.
	NormalizeThinkingLevel(raw string) (string, bool)
}

// ModelHelper provides model configuration access.
type ModelHelper interface {
	EffectiveModel(meta any) string
	ContextWindow(meta any) int
}

// ContextHelper provides context lifecycle management.
type ContextHelper interface {
	MergeDisconnectContext(ctx context.Context) (context.Context, context.CancelFunc)
	BackgroundContext(ctx context.Context) context.Context
}

// CompletionToolCall represents a tool call from a model completion.
type CompletionToolCall struct {
	ID       string
	Name     string
	ArgsJSON string
}

// CompletionResult represents a model completion response.
type CompletionResult struct {
	AssistantMessage openai.ChatCompletionMessageParamUnion
	ToolCalls        []CompletionToolCall
	Done             bool
}

// ChatCompletionAPI provides LLM chat completion access.
type ChatCompletionAPI interface {
	NewCompletion(ctx context.Context, model string, messages []openai.ChatCompletionMessageParamUnion, toolParams any) (*CompletionResult, error)
}

// ToolPolicyHelper provides tool enablement and execution.
type ToolPolicyHelper interface {
	IsToolEnabled(meta any, toolName string) bool
	AllToolDefinitions() []ToolDefinition
	ExecuteToolInContext(ctx context.Context, portal any, meta any, name string, argsJSON string) (string, error)
	ToolsToOpenAIParams(tools []ToolDefinition) any
}

// TextFileHelper provides text file storage operations.
type TextFileHelper interface {
	ReadTextFile(ctx context.Context, agentID string, path string) (content string, filePath string, found bool, err error)
	WriteTextFile(ctx context.Context, portal any, meta any, agentID string, mode string, path string, content string, maxBytes int) (finalPath string, err error)
}

// EmbeddingHelper provides embedding API configuration resolution.
// The caller provides its own configured values; the host fills in defaults
// from provider/proxy configuration where the caller's values are empty.
type EmbeddingHelper interface {
	ResolveOpenAIEmbeddingConfig(apiKey string, baseURL string, headers map[string]string) (string, string, map[string]string)
	ResolveDirectOpenAIEmbeddingConfig(apiKey string, baseURL string, headers map[string]string) (string, string, map[string]string)
	ResolveGeminiEmbeddingConfig(apiKey string, baseURL string, headers map[string]string) (string, string, map[string]string)
}

// OverflowHelper provides overflow handling support.
type OverflowHelper interface {
	SmartTruncatePrompt(prompt []openai.ChatCompletionMessageParamUnion, ratio float64) []openai.ChatCompletionMessageParamUnion
	EstimateTokens(prompt []openai.ChatCompletionMessageParamUnion, model string) int
	CompactorReserveTokens() int
	SilentReplyToken() string
	// OverflowFlushConfig returns the configured overflow-flush settings.
	// Returns (enabled *bool, softThresholdTokens int, prompt string, systemPrompt string).
	OverflowFlushConfig() (enabled *bool, softThresholdTokens int, prompt string, systemPrompt string)
}

// SessionPortalInfo is a generic portal reference for session listing.
type SessionPortalInfo struct {
	Key       string
	PortalKey any
}

// LoginHelper provides login data access and per-login operations.
type LoginHelper interface {
	IsLoggedIn() bool
	SessionPortals(ctx context.Context, loginID string, agentID string) ([]SessionPortalInfo, error)
	LoginDB() any
}
