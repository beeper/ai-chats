package runtime

import (
	"context"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
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
	RequiresPortal bool
	RequiresLogin  bool
	AdminOnly      bool
}

// CommandScope carries command execution context without importing connector internals.
type CommandScope struct {
	Portal *bridgev2.Portal
	Meta   Meta
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
	Portal     *bridgev2.Portal
	Meta       Meta
	SessionKey string
	Force      bool
	Kind       SessionMutationKind
}

// FileChangedEvent is emitted when a file write/edit/apply_patch updates workspace data.
type FileChangedEvent struct {
	Portal *bridgev2.Portal
	Meta   Meta
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
	Portal              *bridgev2.Portal
	Meta                Meta
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
	Portal          *bridgev2.Portal
	Meta            Meta
	Prompt          []openai.ChatCompletionMessageParamUnion
	RequestedTokens int
	ModelMaxTokens  int
	Attempt         int
}

// LoginScope carries per-login cleanup scope.
type LoginScope struct {
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
	RawLogger() zerolog.Logger
	ModuleConfig(name string) map[string]any
	AgentModuleConfig(agentID string, module string) map[string]any

	SavePortal(ctx context.Context, portal *bridgev2.Portal, reason string) error

	IsGroupChat(ctx context.Context, portal *bridgev2.Portal) bool

	RecentMessages(ctx context.Context, portal *bridgev2.Portal, count int) []MessageSummary

	ResolveAgentID(raw string) string
	UserTimezone() (tz string, loc *time.Location)

	EffectiveModel(meta Meta) string
	ContextWindow(meta Meta) int

	NewCompletion(ctx context.Context, model string, messages []openai.ChatCompletionMessageParamUnion, toolParams []openai.ChatCompletionToolUnionParam) (*CompletionResult, error)

	IsToolEnabled(meta Meta, toolName string) bool
	AllToolDefinitions() []ToolDefinition
	ExecuteToolInContext(ctx context.Context, portal *bridgev2.Portal, meta Meta, name string, argsJSON string) (string, error)
	ToolsToOpenAIParams(tools []ToolDefinition) []openai.ChatCompletionToolUnionParam

	ReadTextFile(ctx context.Context, agentID string, path string) (content string, filePath string, found bool, err error)
	WriteTextFile(ctx context.Context, portal *bridgev2.Portal, meta Meta, agentID string, mode string, path string, content string, maxBytes int) (finalPath string, err error)

	SmartTruncatePrompt(prompt []openai.ChatCompletionMessageParamUnion, ratio float64) []openai.ChatCompletionMessageParamUnion
	EstimateTokens(prompt []openai.ChatCompletionMessageParamUnion, model string) int
	CompactorReserveTokens() int
	SilentReplyToken() string
	OverflowFlushConfig() (enabled *bool, softThresholdTokens int, prompt string, systemPrompt string)

	SessionPortals(ctx context.Context, agentID string) ([]SessionPortalInfo, error)
	SessionTranscript(ctx context.Context, portalKey networkid.PortalKey) ([]MessageSummary, error)
}

// Logger is a minimal structured logger abstraction.
type Logger interface {
	Debug(msg string, fields map[string]any)
	Info(msg string, fields map[string]any)
	Warn(msg string, fields map[string]any)
	Error(msg string, fields map[string]any)
}
