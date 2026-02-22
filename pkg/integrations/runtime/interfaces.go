package runtime

import (
	"context"

	"github.com/openai/openai-go/v3"
)

// SettingSource indicates where a setting value came from.
type SettingSource string

const (
	SourceAgentPolicy    SettingSource = "agent_policy"
	SourceRoomOverride   SettingSource = "room_override"
	SourceUserDefault    SettingSource = "user_default"
	SourceProviderConfig SettingSource = "provider_config"
	SourceGlobalDefault  SettingSource = "global_default"
	SourceModelLimit     SettingSource = "model_limitation"
	SourceProviderLimit  SettingSource = "provider_limitation"
)

// ToolDefinition describes a callable tool.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(ctx context.Context, args map[string]any) (string, error)
}

// ToolScope carries integration context without coupling to connector internals.
type ToolScope struct {
	Client any
	Portal any
	Meta   any
}

// ToolCall is a concrete tool execution request.
type ToolCall struct {
	Name        string
	Args        map[string]any
	RawArgsJSON string
	Scope       ToolScope
}

// PromptScope carries prompt-building context without coupling to connector internals.
type PromptScope struct {
	Client any
	Portal any
	Meta   any
}

// ToolIntegration is the pluggable surface for tool definitions/availability/execution.
type ToolIntegration interface {
	Name() string
	ToolDefinitions(ctx context.Context, scope ToolScope) []ToolDefinition
	ExecuteTool(ctx context.Context, call ToolCall) (handled bool, result string, err error)
	ToolAvailability(ctx context.Context, scope ToolScope, toolName string) (known bool, available bool, source SettingSource, reason string)
}

// PromptIntegration is the pluggable surface for prompt/system message augmentation.
type PromptIntegration interface {
	Name() string
	AdditionalSystemMessages(ctx context.Context, scope PromptScope) []openai.ChatCompletionMessageParamUnion
	AugmentPrompt(ctx context.Context, scope PromptScope, prompt []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion
}

// ToolApprovalIntegration is an optional seam for tool approval policy overrides.
type ToolApprovalIntegration interface {
	Name() string
	ToolApprovalRequirement(toolName string, args map[string]any) (handled bool, required bool, action string)
}

// LifecycleIntegration is an optional capability for integrations that need runtime start/stop hooks.
type LifecycleIntegration interface {
	Start(ctx context.Context) error
	Stop()
}

// LoginLifecycleIntegration is an optional capability for integrations that need per-login shutdown hooks.
type LoginLifecycleIntegration interface {
	StopForLogin(bridgeID, loginID string)
}
