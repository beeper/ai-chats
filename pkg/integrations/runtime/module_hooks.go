package runtime

import (
	"context"
	"time"

	"github.com/openai/openai-go/v3"
)

// ModuleHooks is the base contract every integration module implements.
type ModuleHooks interface {
	Name() string
}

// ModuleFactory constructs a module instance from the runtime host.
type ModuleFactory func(host Host) ModuleHooks

// CommandDefinition describes a chat command exposed by a module.
type CommandDefinition struct {
	Name           string
	Description    string
	Args           string
	Aliases        []string
	RequiresPortal bool
	RequiresLogin  bool
	AdminOnly      bool
}

// CommandScope carries command execution context without importing connector internals.
type CommandScope struct {
	Client any
	Portal any
	Meta   any
	Event  any
}

// CommandCall is a concrete command execution request.
type CommandCall struct {
	Name    string
	Args    []string
	RawArgs string
	Scope   CommandScope
	Reply   func(format string, args ...any)
}

// CommandIntegration is the pluggable seam for command definitions/execution.
type CommandIntegration interface {
	Name() string
	CommandDefinitions(ctx context.Context, scope CommandScope) []CommandDefinition
	ExecuteCommand(ctx context.Context, call CommandCall) (handled bool, err error)
}

// SessionMutationKind describes why session context changed.
type SessionMutationKind string

const (
	SessionMutationUnknown SessionMutationKind = "unknown"
	SessionMutationMessage SessionMutationKind = "message"
	SessionMutationReplay  SessionMutationKind = "replay"
	SessionMutationEdit    SessionMutationKind = "edit"
	SessionMutationDelete  SessionMutationKind = "delete"
)

// SessionMutationEvent is emitted when chat/session data changes.
type SessionMutationEvent struct {
	Client     any
	Portal     any
	Meta       any
	SessionKey string
	Force      bool
	Kind       SessionMutationKind
}

// FileChangedEvent is emitted when a file write/edit/apply_patch updates workspace data.
type FileChangedEvent struct {
	Client any
	Portal any
	Meta   any
	Path   string
}

// EventIntegration consumes session/file events.
type EventIntegration interface {
	Name() string
	OnSessionMutation(ctx context.Context, evt SessionMutationEvent)
	OnFileChanged(ctx context.Context, evt FileChangedEvent)
}

// CompactionLifecyclePhase describes runtime compaction lifecycle hooks.
type CompactionLifecyclePhase string

const (
	CompactionLifecyclePreFlush CompactionLifecyclePhase = "pre_flush"
	CompactionLifecycleStart    CompactionLifecyclePhase = "start"
	CompactionLifecycleEnd      CompactionLifecyclePhase = "end"
	CompactionLifecycleFail     CompactionLifecyclePhase = "fail"
	CompactionLifecycleRefresh  CompactionLifecyclePhase = "post_refresh"
)

// CompactionLifecycleEvent provides compaction lifecycle details to integrations.
type CompactionLifecycleEvent struct {
	Client              any
	Portal              any
	Meta                any
	Phase               CompactionLifecyclePhase
	Attempt             int
	ContextWindowTokens int
	RequestedTokens     int
	PromptTokens        int
	MessagesBefore      int
	MessagesAfter       int
	TokensBefore        int
	TokensAfter         int
	DroppedCount        int
	Reason              string
	WillRetry           bool
	Error               string
}

// CompactionLifecycleIntegration consumes compaction lifecycle events.
type CompactionLifecycleIntegration interface {
	Name() string
	OnCompactionLifecycle(ctx context.Context, evt CompactionLifecycleEvent)
}

// ContextOverflowCall contains context-overflow retry state.
type ContextOverflowCall struct {
	Client          any
	Portal          any
	Meta            any
	Prompt          []openai.ChatCompletionMessageParamUnion
	RequestedTokens int
	ModelMaxTokens  int
	Attempt         int
}

// LoginScope carries per-login cleanup scope.
type LoginScope struct {
	Client   any
	Login    any
	BridgeID string
	LoginID  string
}

// LoginPurgeIntegration performs per-login data cleanup.
type LoginPurgeIntegration interface {
	Name() string
	PurgeForLogin(ctx context.Context, scope LoginScope) error
}

// Host is the runtime surface shared by integration modules.
// It is intentionally direct: modules call host methods rather than retrieving
// nested capability objects or type-asserting optional host adapters.
type Host interface {
	Logger() Logger
	RawLogger() any
	Now() time.Time
	ResolvePortalByRoomID(ctx context.Context, roomID string) any
	ResolveDefaultPortal(ctx context.Context) any
	ResolveLastActivePortal(ctx context.Context, agentID string) any
	DispatchInternalMessage(ctx context.Context, portal any, meta any, message string, source string) error
	SendAssistantMessage(ctx context.Context, portal any, body string) error
	RequestNow(ctx context.Context, reason string)
	ToolDefinitionByName(name string) (ToolDefinition, bool)
	ExecuteBuiltinTool(ctx context.Context, scope ToolScope, name string, rawArgsJSON string) (string, error)
	ResolveWorkspaceDir() string
	BridgeDB() any
	BridgeID() string
	LoginID() string
	ModuleEnabled(name string) bool
	ModuleConfig(name string) map[string]any
	AgentModuleConfig(agentID string, module string) map[string]any

	GetOrCreatePortal(ctx context.Context, portalID string, receiver string, displayName string, setupMeta func(meta any)) (portal any, roomID string, err error)
	SavePortal(ctx context.Context, portal any, reason string) error
	PortalRoomID(portal any) string
	PortalKeyString(portal any) string

	GetModuleMeta(meta any, key string) any
	SetModuleMeta(meta any, key string, value any)
	AgentIDFromMeta(meta any) string
	CompactionCount(meta any) int
	IsGroupChat(ctx context.Context, portal any) bool
	IsInternalRoom(meta any) bool
	PortalMeta(portal any) any
	CloneMeta(portal any) any
	SetMetaField(meta any, key string, value any)

	RecentMessages(ctx context.Context, portal any, count int) []MessageSummary
	LastAssistantMessage(ctx context.Context, portal any) (id string, timestamp int64)
	WaitForAssistantMessage(ctx context.Context, portal any, afterID string, afterTS int64) (*AssistantMessageInfo, bool)

	RunHeartbeatOnce(ctx context.Context, reason string) (status string, reasonMsg string)
	ResolveHeartbeatSessionPortal(agentID string) (portal any, sessionKey string, err error)
	ResolveHeartbeatSessionKey(agentID string) string
	HeartbeatAckMaxChars(agentID string) int
	EnqueueSystemEvent(sessionKey string, text string, agentID string)
	PersistSystemEvents()
	ResolveLastTarget(agentID string) (channel string, target string, ok bool)

	ResolveAgentID(raw string, fallbackDefault string) string
	NormalizeAgentID(raw string) string
	AgentExists(normalizedID string) bool
	DefaultAgentID() string
	AgentTimeoutSeconds() int
	UserTimezone() (tz string, loc *time.Location)
	NormalizeThinkingLevel(raw string) (string, bool)

	EffectiveModel(meta any) string
	ContextWindow(meta any) int

	MergeDisconnectContext(ctx context.Context) (context.Context, context.CancelFunc)
	BackgroundContext(ctx context.Context) context.Context

	NewCompletion(ctx context.Context, model string, messages []openai.ChatCompletionMessageParamUnion, toolParams any) (*CompletionResult, error)

	IsToolEnabled(meta any, toolName string) bool
	AllToolDefinitions() []ToolDefinition
	ExecuteToolInContext(ctx context.Context, portal any, meta any, name string, argsJSON string) (string, error)
	ToolsToOpenAIParams(tools []ToolDefinition) any

	ReadTextFile(ctx context.Context, agentID string, path string) (content string, filePath string, found bool, err error)
	WriteTextFile(ctx context.Context, portal any, meta any, agentID string, mode string, path string, content string, maxBytes int) (finalPath string, err error)

	SmartTruncatePrompt(prompt []openai.ChatCompletionMessageParamUnion, ratio float64) []openai.ChatCompletionMessageParamUnion
	EstimateTokens(prompt []openai.ChatCompletionMessageParamUnion, model string) int
	CompactorReserveTokens() int
	SilentReplyToken() string
	OverflowFlushConfig() (enabled *bool, softThresholdTokens int, prompt string, systemPrompt string)

	IsLoggedIn() bool
	SessionPortals(ctx context.Context, loginID string, agentID string) ([]SessionPortalInfo, error)
	LoginDB() any
}

// Logger is a minimal structured logger abstraction.
type Logger interface {
	Debug(msg string, fields map[string]any)
	Info(msg string, fields map[string]any)
	Warn(msg string, fields map[string]any)
	Error(msg string, fields map[string]any)
}
