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

// Host is the generic runtime host surface shared by modules.
// Module packages may use additional optional interfaces via type assertions.
type Host interface {
	Logger() Logger
	Now() time.Time
	StoreBackend() StoreBackend
	PortalResolver() PortalResolver
	Dispatch() Dispatch
	SessionStore() SessionStore
	Heartbeat() Heartbeat
	ToolExec() ToolExec
	PromptContext() PromptContext
	DBAccess() DBAccess
	ConfigLookup() ConfigLookup
}

// StoreEntry mirrors state-store entries in a generic form.
type StoreEntry struct {
	Key  string
	Data []byte
}

// StoreBackend exposes generic key-value state storage.
type StoreBackend interface {
	Read(ctx context.Context, key string) ([]byte, bool, error)
	Write(ctx context.Context, key string, data []byte) error
	List(ctx context.Context, prefix string) ([]StoreEntry, error)
}

// PortalResolver provides room/portal lookup utilities.
type PortalResolver interface {
	ResolvePortalByRoomID(ctx context.Context, roomID string) any
	ResolveDefaultPortal(ctx context.Context) any
	ResolveLastActivePortal(ctx context.Context, agentID string) any
}

// Dispatch provides generic event/message dispatch hooks.
type Dispatch interface {
	DispatchInternalMessage(ctx context.Context, portal any, meta any, message string, source string) error
	SendAssistantMessage(ctx context.Context, portal any, body string) error
}

// SessionStore provides per-module session metadata persistence.
type SessionStore interface {
	Update(ctx context.Context, key string, updater func(raw map[string]any) map[string]any)
}

// Heartbeat exposes generic heartbeat controls.
type Heartbeat interface {
	RequestNow(ctx context.Context, reason string)
}

// ToolExec provides bridge tool runtime helpers.
type ToolExec interface {
	ToolDefinitionByName(name string) (ToolDefinition, bool)
	ExecuteBuiltinTool(ctx context.Context, scope ToolScope, name string, rawArgsJSON string) (string, error)
}

// PromptContext provides prompt/workspace contextual helpers.
type PromptContext interface {
	ResolveWorkspaceDir() string
}

// DBAccess exposes bridge DB identity and low-level access.
type DBAccess interface {
	BridgeDB() any
	BridgeID() string
	LoginID() string
}

// ConfigLookup resolves integration/module config flags.
type ConfigLookup interface {
	ModuleEnabled(name string) bool
	ModuleConfig(name string) map[string]any
	AgentModuleConfig(agentID string, module string) map[string]any
}

// Logger is a minimal structured logger abstraction.
type Logger interface {
	Debug(msg string, fields map[string]any)
	Info(msg string, fields map[string]any)
	Warn(msg string, fields map[string]any)
	Error(msg string, fields map[string]any)
}
