package ai

import (
	"reflect"

	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-chats/pkg/matrixevents"
)

func init() {
	event.TypeMap[AIRoomInfoEventType] = reflect.TypeOf(AIRoomInfoContent{})
}

// StreamEventMessageType is the unified event type for AI streaming updates (ephemeral).
var StreamEventMessageType = matrixevents.StreamEventMessageType

// AIRoomInfoEventType stores lightweight room metadata for AI rooms.
var AIRoomInfoEventType = matrixevents.AIRoomInfoEventType

type ToolStatus = matrixevents.ToolStatus

const (
	ToolStatusPending   = matrixevents.ToolStatusPending
	ToolStatusRunning   = matrixevents.ToolStatusRunning
	ToolStatusCompleted = matrixevents.ToolStatusCompleted
	ToolStatusFailed    = matrixevents.ToolStatusFailed
	ToolStatusTimeout   = matrixevents.ToolStatusTimeout
	ToolStatusCancelled = matrixevents.ToolStatusCancelled
)

type ResultStatus = matrixevents.ResultStatus

const (
	ResultStatusSuccess = matrixevents.ResultStatusSuccess
	ResultStatusError   = matrixevents.ResultStatusError
	ResultStatusPartial = matrixevents.ResultStatusPartial
	ResultStatusDenied  = matrixevents.ResultStatusDenied
)

// SettingSource indicates where a setting or availability decision came from.
type SettingSource string

const (
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

// ModelInfo describes a single AI model's capabilities
type ModelInfo struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Provider            string   `json:"provider"`
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

// AIRoomInfoContent identifies the AI room surface for clients and sync state stores.
type AIRoomInfoContent struct {
	Type string `json:"type"`
}
