package connector

import (
	"reflect"

	"maunium.net/go/mautrix/event"
	_ "maunium.net/go/mautrix/event/cmdschema"

	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
	"github.com/beeper/ai-bridge/pkg/matrixevents"
)

// init registers custom AI event types with mautrix's TypeMap
// so the state store can properly parse them during sync
func init() {
	event.TypeMap[AgentsEventType] = reflect.TypeOf(AgentsEventContent{})
}

// StreamEventMessageType is the unified event type for AI streaming updates (ephemeral).
var StreamEventMessageType = matrixevents.StreamEventMessageType

// CompactionStatusEventType notifies clients about context compaction
var CompactionStatusEventType = matrixevents.CompactionStatusEventType

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

// SettingSource indicates where a setting or availability decision came from.
type SettingSource string

const (
	SourceAgentPolicy    SettingSource = "agent_policy"
	SourceProviderConfig SettingSource = "provider_config"
	SourceGlobalDefault  SettingSource = "global_default"
	SourceModelLimit     SettingSource = "model_limitation"
	SourceProviderLimit  SettingSource = "provider_limitation"
)

// ToolInfo describes a tool and its status for internal UI/config rendering.
type ToolInfo struct {
	Name        string        `json:"name"`
	DisplayName string        `json:"display_name"`
	Type        string        `json:"type"`
	Description string        `json:"description,omitempty"`
	Enabled     bool          `json:"enabled"`
	Available   bool          `json:"available"`
	Source      SettingSource `json:"source,omitempty"`
	Reason      string        `json:"reason,omitempty"`
}

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
	BeeperAIKey = matrixevents.BeeperAIKey
)

// CommandDescriptionEventType is the state event type for MSC4391 command descriptions.
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
