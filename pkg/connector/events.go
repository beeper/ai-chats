package connector

import (
	"reflect"

	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
	"github.com/beeper/ai-bridge/pkg/matrixevents"
)

// init registers custom AI event types with mautrix's TypeMap
// so the state store can properly parse them during sync
func init() {
	event.TypeMap[RoomCapabilitiesEventType] = reflect.TypeOf(RoomCapabilitiesEventContent{})
	event.TypeMap[RoomSettingsEventType] = reflect.TypeOf(RoomSettingsEventContent{})
	event.TypeMap[ModelCapabilitiesEventType] = reflect.TypeOf(ModelCapabilitiesEventContent{})
	event.TypeMap[AgentsEventType] = reflect.TypeOf(AgentsEventContent{})
}

// ToolCallEventType represents a tool invocation
var ToolCallEventType = matrixevents.ToolCallEventType

// ToolResultEventType represents a tool execution result
var ToolResultEventType = matrixevents.ToolResultEventType

// StreamEventMessageType is the unified event type for AI streaming updates (ephemeral).
var StreamEventMessageType = matrixevents.StreamEventMessageType

// CompactionStatusEventType notifies clients about context compaction
var CompactionStatusEventType = matrixevents.CompactionStatusEventType

// RoomCapabilitiesEventType is the Matrix state event type for bridge-controlled capabilities
// Protected by power levels (100) so only the bridge bot can modify
var RoomCapabilitiesEventType = matrixevents.RoomCapabilitiesEventType

// RoomSettingsEventType is the Matrix state event type for user-editable settings
// Normal power level (0) so users can modify
var RoomSettingsEventType = matrixevents.RoomSettingsEventType

// ModelCapabilitiesEventType is the Matrix state event type for broadcasting available models
var ModelCapabilitiesEventType = matrixevents.ModelCapabilitiesEventType

// AgentsEventType configures active agents in a room
var AgentsEventType = matrixevents.AgentsEventType

type ToolStatus = matrixevents.ToolStatus

const (
	ToolStatusPending          = matrixevents.ToolStatusPending
	ToolStatusRunning          = matrixevents.ToolStatusRunning
	ToolStatusCompleted        = matrixevents.ToolStatusCompleted
	ToolStatusFailed           = matrixevents.ToolStatusFailed
	ToolStatusTimeout          = matrixevents.ToolStatusTimeout
	ToolStatusCancelled        = matrixevents.ToolStatusCancelled
	ToolStatusApprovalRequired = matrixevents.ToolStatusApprovalRequired
)

type ResultStatus = matrixevents.ResultStatus

const (
	ResultStatusSuccess = matrixevents.ResultStatusSuccess
	ResultStatusError   = matrixevents.ResultStatusError
	ResultStatusPartial = matrixevents.ResultStatusPartial
	ResultStatusDenied  = matrixevents.ResultStatusDenied
)

type ToolType = matrixevents.ToolType

const (
	ToolTypeBuiltin  = matrixevents.ToolTypeBuiltin
	ToolTypeProvider = matrixevents.ToolTypeProvider
	ToolTypeFunction = matrixevents.ToolTypeFunction
	ToolTypeMCP      = matrixevents.ToolTypeMCP
)

// ReasoningEffortOption represents an available reasoning effort level
type ReasoningEffortOption struct {
	Value string `json:"value"` // minimal, low, medium, high, xhigh
	Label string `json:"label"` // Display name
}

// SettingSource indicates where a setting value came from
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

// SettingExplanation describes why a setting has its current value
type SettingExplanation struct {
	Value  any           `json:"value"`
	Source SettingSource `json:"source"`
	Reason string        `json:"reason,omitempty"` // Only when limited/unavailable
}

// EffectiveSettings shows current values with source explanations
type EffectiveSettings struct {
	Model           SettingExplanation `json:"model"`
	SystemPrompt    SettingExplanation `json:"system_prompt"`
	Temperature     SettingExplanation `json:"temperature"`
	ReasoningEffort SettingExplanation `json:"reasoning_effort"`
}

// RoomCapabilitiesEventContent represents bridge-controlled room capabilities
// This is protected by power levels (100) so only the bridge bot can modify
type RoomCapabilitiesEventContent struct {
	Capabilities           *ModelCapabilities      `json:"capabilities,omitempty"`
	AvailableTools         []ToolInfo              `json:"available_tools,omitempty"`
	ReasoningEffortOptions []ReasoningEffortOption `json:"reasoning_effort_options,omitempty"`
	Provider               string                  `json:"provider,omitempty"`
	EffectiveSettings      *EffectiveSettings      `json:"effective_settings,omitempty"`
}

// RoomSettingsEventContent represents user-editable room settings
// This uses normal power levels (0) so users can modify
type RoomSettingsEventContent struct {
	Model               string   `json:"model,omitempty"`
	SystemPrompt        string   `json:"system_prompt,omitempty"`
	Temperature         *float64 `json:"temperature,omitempty"`
	MaxContextMessages  int      `json:"max_context_messages,omitempty"`
	MaxCompletionTokens int      `json:"max_completion_tokens,omitempty"`
	ReasoningEffort     string   `json:"reasoning_effort,omitempty"`
	ConversationMode    string   `json:"conversation_mode,omitempty"` // "messages" or "responses"
	AgentID             string   `json:"agent_id,omitempty"`
	EmitThinking        *bool    `json:"emit_thinking,omitempty"`
	EmitToolArgs        *bool    `json:"emit_tool_args,omitempty"`
}

// ToolInfo describes a tool and its status for room state broadcasting
type ToolInfo struct {
	Name        string        `json:"name"`
	DisplayName string        `json:"display_name"` // Human-readable name for UI
	Type        string        `json:"type"`         // "builtin", "provider", "plugin", "mcp"
	Description string        `json:"description,omitempty"`
	Enabled     bool          `json:"enabled"`
	Available   bool          `json:"available"`        // Based on model capabilities and provider
	Source      SettingSource `json:"source,omitempty"` // Where enabled state came from
	Reason      string        `json:"reason,omitempty"` // Only when limited/unavailable
}

// ModelCapabilitiesEventContent represents available models and their capabilities
type ModelCapabilitiesEventContent struct {
	AvailableModels []ModelInfo `json:"available_models"`
}

// Tool constants for model capabilities
const (
	ToolWebSearch       = "web_search"
	ToolFunctionCalling = "function_calling"
)

// Relation types
const (
	RelReplace   = matrixevents.RelReplace
	RelReference = matrixevents.RelReference
	RelThread    = matrixevents.RelThread
)

// Content field keys
const (
	BeeperAIKey           = matrixevents.BeeperAIKey
	BeeperAIToolCallKey   = matrixevents.BeeperAIToolCallKey
	BeeperAIToolResultKey = matrixevents.BeeperAIToolResultKey
	BeeperActionHintsKey  = matrixevents.BeeperActionHintsKey
)

// CommandDescriptionEventType is the state event type for com.beeper.command_description.
var CommandDescriptionEventType = matrixevents.CommandDescriptionEventType

// ModelInfo describes a single AI model's capabilities
type ModelInfo struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Provider            string   `json:"provider"`
	API                 string   `json:"api,omitempty"`
	Description         string   `json:"description,omitempty"`
	SupportsVision      bool     `json:"supports_vision"`
	SupportsToolCalling bool     `json:"supports_tool_calling"`
	SupportsPDF         bool     `json:"supports_pdf,omitempty"`
	SupportsReasoning   bool     `json:"supports_reasoning"`
	SupportsWebSearch   bool     `json:"supports_web_search"`
	SupportsImageGen    bool     `json:"supports_image_gen,omitempty"`
	SupportsAudio       bool     `json:"supports_audio,omitempty"`
	SupportsVideo       bool     `json:"supports_video,omitempty"`
	ContextWindow       int      `json:"context_window,omitempty"`
	MaxOutputTokens     int      `json:"max_output_tokens,omitempty"`
	AvailableTools      []string `json:"available_tools,omitempty"`
}

// AgentsEventContent configures active agents in a room
type AgentsEventContent struct {
	Agents        []AgentConfig        `json:"agents"`
	Orchestration *OrchestrationConfig `json:"orchestration,omitempty"`
}

// AgentConfig describes an AI agent
type AgentConfig struct {
	AgentID     string   `json:"agent_id"`
	Name        string   `json:"name"`
	Model       string   `json:"model"`
	UserID      string   `json:"user_id"` // Matrix user ID for this agent
	Role        string   `json:"role"`    // "primary", "specialist"
	Description string   `json:"description,omitempty"`
	AvatarURL   string   `json:"avatar_url,omitempty"` // mxc:// URL
	Triggers    []string `json:"triggers,omitempty"`   // e.g., ["@researcher", "/research"]
}

// OrchestrationConfig defines how agents work together
type OrchestrationConfig struct {
	Mode          string `json:"mode"` // "user_directed", "auto"
	AllowParallel bool   `json:"allow_parallel"`
	MaxConcurrent int    `json:"max_concurrent,omitempty"`
}

// AgentDefinitionContent stores agent configuration in Matrix state events.
// This is the serialized form of agents.AgentDefinition for Matrix storage.
type AgentDefinitionContent struct {
	ID              string                       `json:"id"`
	Name            string                       `json:"name"`
	Description     string                       `json:"description,omitempty"`
	AvatarURL       string                       `json:"avatar_url,omitempty"`
	Model           string                       `json:"model,omitempty"`
	ModelFallback   []string                     `json:"model_fallback,omitempty"`
	SystemPrompt    string                       `json:"system_prompt,omitempty"`
	PromptMode      string                       `json:"prompt_mode,omitempty"`
	Tools           *toolpolicy.ToolPolicyConfig `json:"tools,omitempty"`
	Temperature     float64                      `json:"temperature,omitempty"`
	ReasoningEffort string                       `json:"reasoning_effort,omitempty"`
	IdentityName    string                       `json:"identity_name,omitempty"`
	IdentityPersona string                       `json:"identity_persona,omitempty"`
	IsPreset        bool                         `json:"is_preset,omitempty"`
	MemorySearch    any                          `json:"memory_search,omitempty"`
	HeartbeatPrompt string                       `json:"heartbeat_prompt,omitempty"`
	CreatedAt       int64                        `json:"created_at"`
	UpdatedAt       int64                        `json:"updated_at"`
}
