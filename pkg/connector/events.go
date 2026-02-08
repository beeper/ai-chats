package connector

import (
	"reflect"

	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/agents"
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

// AssistantTurnEventType is the container event for an assistant's response
var AssistantTurnEventType = matrixevents.AssistantTurnEventType

// ToolCallEventType represents a tool invocation
var ToolCallEventType = matrixevents.ToolCallEventType

// ToolResultEventType represents a tool execution result
var ToolResultEventType = matrixevents.ToolResultEventType

// AIErrorEventType represents AI generation errors that are part of conversation
var AIErrorEventType = matrixevents.AIErrorEventType

// TurnCancelledEventType represents a cancelled turn
var TurnCancelledEventType = matrixevents.TurnCancelledEventType

// AgentHandoffEventType represents a handoff between agents
var AgentHandoffEventType = matrixevents.AgentHandoffEventType

// StepBoundaryEventType represents multi-step boundaries within a turn
var StepBoundaryEventType = matrixevents.StepBoundaryEventType

// StreamDeltaEventType is the custom event type for streaming token updates (ephemeral).
var StreamDeltaEventType = matrixevents.StreamDeltaEventType

// StreamEventMessageType is the unified event type for AI streaming updates (ephemeral).
var StreamEventMessageType = matrixevents.StreamEventMessageType

// GenerationStatusEventType provides rich status updates during generation
var GenerationStatusEventType = matrixevents.GenerationStatusEventType

// ToolProgressEventType provides tool execution progress updates
var ToolProgressEventType = matrixevents.ToolProgressEventType

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

// StreamContentType identifies the type of content in a stream delta
type StreamContentType string

const (
	StreamContentText       StreamContentType = "text"
	StreamContentReasoning  StreamContentType = "reasoning"
	StreamContentToolInput  StreamContentType = "tool_input"
	StreamContentToolResult StreamContentType = "tool_result"
	StreamContentCode       StreamContentType = "code"
	StreamContentImage      StreamContentType = "image"
)

// TurnStatus represents the state of an assistant turn
type TurnStatus string

const (
	TurnStatusPending    TurnStatus = "pending"
	TurnStatusThinking   TurnStatus = "thinking"
	TurnStatusGenerating TurnStatus = "generating"
	TurnStatusToolUse    TurnStatus = "tool_use"
	TurnStatusCompleted  TurnStatus = "completed"
	TurnStatusFailed     TurnStatus = "failed"
	TurnStatusCancelled  TurnStatus = "cancelled"
)

// ToolStatus represents the state of a tool call
type ToolStatus string

const (
	ToolStatusPending   ToolStatus = "pending"
	ToolStatusRunning   ToolStatus = "running"
	ToolStatusCompleted ToolStatus = "completed"
	ToolStatusFailed    ToolStatus = "failed"
	ToolStatusTimeout   ToolStatus = "timeout"
	ToolStatusCancelled        ToolStatus = "cancelled"
	ToolStatusApprovalRequired ToolStatus = "approval_required"
)

// ResultStatus represents the status of a tool result
type ResultStatus string

const (
	ResultStatusSuccess ResultStatus = "success"
	ResultStatusError   ResultStatus = "error"
	ResultStatusPartial ResultStatus = "partial"
	ResultStatusDenied  ResultStatus = "denied"
)

// ToolType identifies the category of tool
type ToolType string

const (
	ToolTypeBuiltin  ToolType = "builtin"
	ToolTypeProvider ToolType = "provider"
	ToolTypeFunction ToolType = "function"
	ToolTypeMCP      ToolType = "mcp"
)

const (
	// Retryable errors
	ErrorContextTooLong = "context_too_long"
	ErrorContentFilter  = "content_filter"
	ErrorToolFailed     = "tool_failed"
	ErrorToolTimeout    = "tool_timeout"

	// Non-retryable errors
	ErrorCancelled    = "cancelled"
	ErrorInvalidInput = "invalid_input"
)

// AssistantTurnContent represents the content of an assistant turn event
type AssistantTurnContent struct {
	// Standard Matrix fallback fields
	Body          string `json:"body"`
	MsgType       string `json:"msgtype"`
	Format        string `json:"format,omitempty"`
	FormattedBody string `json:"formatted_body,omitempty"`

	// AI-specific metadata
	AI *AssistantTurnAI `json:"com.beeper.ai,omitempty"`
}

// AssistantTurnAI contains the AI-specific metadata for an assistant turn
type AssistantTurnAI struct {
	TurnID       string     `json:"turn_id"`
	AgentID      string     `json:"agent_id,omitempty"`
	Model        string     `json:"model"`
	Status       TurnStatus `json:"status"`
	FinishReason string     `json:"finish_reason,omitempty"`

	// Embedded thinking (not separate event)
	Thinking *ThinkingContent `json:"thinking,omitempty"`

	// Token usage
	Usage *EventUsageInfo `json:"usage,omitempty"`

	// Related events
	ToolCalls []string `json:"tool_calls,omitempty"`
	Images    []string `json:"images,omitempty"`

	// Timing information
	Timing *TimingInfo `json:"timing,omitempty"`

	// Annotations/citations
	Annotations []Annotation `json:"annotations,omitempty"`
}

// ThinkingContent represents embedded thinking/reasoning content
type ThinkingContent struct {
	Content    string `json:"content,omitempty"`
	TokenCount int    `json:"token_count,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

// EventUsageInfo contains token usage information for Matrix events
// This is separate from the internal UsageInfo in provider.go to allow
// different serialization formats (int64 for Matrix JSON vs int for internal use)
type EventUsageInfo struct {
	PromptTokens     int64 `json:"prompt_tokens,omitempty"`
	CompletionTokens int64 `json:"completion_tokens,omitempty"`
	ReasoningTokens  int64 `json:"reasoning_tokens,omitempty"`
}

// TimingInfo contains timing information for events
type TimingInfo struct {
	StartedAt    int64 `json:"started_at,omitempty"`     // Unix ms
	FirstTokenAt int64 `json:"first_token_at,omitempty"` // Unix ms
	CompletedAt  int64 `json:"completed_at,omitempty"`   // Unix ms
}

// Annotation represents a citation or reference in the text
type Annotation struct {
	Type      string            `json:"type"`            // "citation", "reference"
	Index     int               `json:"index,omitempty"` // Citation number [1], [2], etc.
	StartChar int               `json:"start_char,omitempty"`
	EndChar   int               `json:"end_char,omitempty"`
	Source    *AnnotationSource `json:"source,omitempty"`
}

// AnnotationSource provides source information for a citation
type AnnotationSource struct {
	Type     string `json:"type"` // "web", "document", "file"
	URL      string `json:"url,omitempty"`
	Title    string `json:"title,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	Filename string `json:"filename,omitempty"`
	Page     int    `json:"page,omitempty"`
}

// ToolCallContent represents a tool call timeline event
type ToolCallContent struct {
	// Standard Matrix fallback
	Body    string `json:"body"`
	MsgType string `json:"msgtype"`

	// Tool call details
	ToolCall *ToolCallData `json:"com.beeper.ai.tool_call"`
}

// ToolCallData contains the tool call metadata
type ToolCallData struct {
	CallID   string     `json:"call_id"`
	TurnID   string     `json:"turn_id"`
	AgentID  string     `json:"agent_id,omitempty"`
	ToolName string     `json:"tool_name"`
	ToolType ToolType   `json:"tool_type"`
	Status   ToolStatus `json:"status"`

	// Input arguments (fully accumulated)
	Input map[string]any `json:"input,omitempty"`

	// Display hints
	Display *ToolDisplay `json:"display,omitempty"`

	// Reference to result event (set after completion)
	ResultEvent string `json:"result_event,omitempty"`

	// MCP-specific fields
	MCPServer string `json:"mcp_server,omitempty"`

	// Timing
	Timing *TimingInfo `json:"timing,omitempty"`

	// Approval flow
	RequiresApproval bool          `json:"requires_approval,omitempty"`
	Approval         *ApprovalInfo `json:"approval,omitempty"`
}

// ToolDisplay contains display hints for tool rendering
type ToolDisplay struct {
	Title     string `json:"title,omitempty"`
	Icon      string `json:"icon,omitempty"` // mxc:// URL
	Collapsed bool   `json:"collapsed,omitempty"`
}

// ApprovalInfo contains approval request details
type ApprovalInfo struct {
	Reason  string   `json:"reason,omitempty"`
	Actions []string `json:"actions,omitempty"`
}

// ToolResultContent represents a tool result timeline event
type ToolResultContent struct {
	// Standard Matrix fallback
	Body          string `json:"body"`
	MsgType       string `json:"msgtype"`
	Format        string `json:"format,omitempty"`
	FormattedBody string `json:"formatted_body,omitempty"`

	// Tool result details
	ToolResult *ToolResultData `json:"com.beeper.ai.tool_result"`
}

// ToolResultData contains the tool result metadata
type ToolResultData struct {
	CallID   string       `json:"call_id"`
	TurnID   string       `json:"turn_id"`
	AgentID  string       `json:"agent_id,omitempty"`
	ToolName string       `json:"tool_name"`
	Status   ResultStatus `json:"status"`

	// Output data
	Output map[string]any `json:"output,omitempty"`

	// Artifacts (files, images generated by tool)
	Artifacts []ToolArtifact `json:"artifacts,omitempty"`

	// Display hints
	Display *ToolResultDisplay `json:"display,omitempty"`
}

// ToolArtifact represents a file or image generated by a tool
type ToolArtifact struct {
	Type     string `json:"type"` // "file", "image"
	MxcURI   string `json:"mxc_uri,omitempty"`
	Filename string `json:"filename,omitempty"`
	Mimetype string `json:"mimetype,omitempty"`
	Size     int    `json:"size,omitempty"`
}

// ToolResultDisplay contains display hints for tool result rendering
type ToolResultDisplay struct {
	Format          string `json:"format,omitempty"` // "search_results", "code_output", etc.
	Expandable      bool   `json:"expandable,omitempty"`
	DefaultExpanded bool   `json:"default_expanded,omitempty"`
	ShowStdout      bool   `json:"show_stdout,omitempty"`
	ShowArtifacts   bool   `json:"show_artifacts,omitempty"`
}

// AIErrorContent represents an AI error timeline event
type AIErrorContent struct {
	Body    string `json:"body"`
	MsgType string `json:"msgtype"`

	Error *AIErrorData `json:"com.beeper.ai.error"`
}

// AIErrorData contains error details
type AIErrorData struct {
	TurnID       string `json:"turn_id,omitempty"`
	AgentID      string `json:"agent_id,omitempty"`
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message"`
	Retryable    bool   `json:"retryable"`
	Suggestion   string `json:"suggestion,omitempty"`
}

// TurnCancelledContent represents a cancelled turn event
type TurnCancelledContent struct {
	TurnID             string   `json:"turn_id"`
	AgentID            string   `json:"agent_id,omitempty"`
	CancelledAt        int64    `json:"cancelled_at"` // Unix ms
	Reason             string   `json:"reason,omitempty"`
	PartialContent     string   `json:"partial_content,omitempty"`
	ToolCallsCancelled []string `json:"tool_calls_cancelled,omitempty"`
}

// AgentHandoffContent represents an agent handoff event
type AgentHandoffContent struct {
	Body    string `json:"body"`
	MsgType string `json:"msgtype"`

	Handoff *AgentHandoffData `json:"com.beeper.ai.agent_handoff"`
}

// AgentHandoffData contains handoff details
type AgentHandoffData struct {
	FromAgent string         `json:"from_agent"`
	ToAgent   string         `json:"to_agent"`
	FromTurn  string         `json:"from_turn,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Context   map[string]any `json:"context,omitempty"`
}

// StepBoundaryContent represents a step boundary within a turn
type StepBoundaryContent struct {
	TurnID            string       `json:"turn_id"`
	AgentID           string       `json:"agent_id,omitempty"`
	StepNumber        int          `json:"step_number"`
	StepType          string       `json:"step_type"` // "tool_response_processed", etc.
	PreviousToolCalls []string     `json:"previous_tool_calls,omitempty"`
	Display           *StepDisplay `json:"display,omitempty"`
}

// StepDisplay contains display hints for step boundaries
type StepDisplay struct {
	Label string `json:"label,omitempty"`
}

// StreamDeltaContent represents a streaming delta event
type StreamDeltaContent struct {
	TurnID      string            `json:"turn_id"`
	AgentID     string            `json:"agent_id,omitempty"`
	TargetEvent string            `json:"target_event,omitempty"` // Event ID being updated
	ContentType StreamContentType `json:"content_type"`
	Delta       string            `json:"delta"`
	Seq         int               `json:"seq"`

	// For tool_input streaming
	CallID   string `json:"call_id,omitempty"`
	ToolName string `json:"tool_name,omitempty"`

	// Cursor information
	Cursor *StreamCursor `json:"cursor,omitempty"`
}

// StreamCursor provides position information for streaming
type StreamCursor struct {
	BlockType  string `json:"block_type,omitempty"` // "text", "code", etc.
	CharOffset int    `json:"char_offset,omitempty"`
	Field      string `json:"field,omitempty"` // For tool_input, which field
}

// GenerationStatusContent represents a generation status update
type GenerationStatusContent struct {
	TurnID        string `json:"turn_id"`
	AgentID       string `json:"agent_id,omitempty"`
	TargetEvent   string `json:"target_event,omitempty"`
	Status        string `json:"status"` // "starting", "thinking", "generating", "tool_use", etc.
	StatusMessage string `json:"status_message,omitempty"`

	Details  *GenerationDetails  `json:"details,omitempty"`
	Progress *GenerationProgress `json:"progress,omitempty"`
	Display  *StatusDisplay      `json:"display,omitempty"`

	// For collaboration
	Collaboration *CollaborationInfo `json:"collaboration,omitempty"`
}

// GenerationDetails provides detailed status information
type GenerationDetails struct {
	CurrentTool    string `json:"current_tool,omitempty"`
	CallID         string `json:"call_id,omitempty"`
	ToolsCompleted int    `json:"tools_completed,omitempty"`
	ToolsTotal     int    `json:"tools_total,omitempty"`
}

// GenerationProgress tracks token generation progress
type GenerationProgress struct {
	TokensGenerated int `json:"tokens_generated,omitempty"`
	ThinkingTokens  int `json:"thinking_tokens,omitempty"`
}

// StatusDisplay contains display hints for status indicators
type StatusDisplay struct {
	Icon      string `json:"icon,omitempty"`
	Animation string `json:"animation,omitempty"` // "pulse", "spin", etc.
	Color     string `json:"color,omitempty"`
}

// CollaborationInfo contains multi-agent collaboration status
type CollaborationInfo struct {
	Orchestrator string                     `json:"orchestrator,omitempty"`
	Participants []CollaborationParticipant `json:"participants,omitempty"`
}

// CollaborationParticipant represents an agent in a collaboration
type CollaborationParticipant struct {
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`
	Task    string `json:"task,omitempty"`
}

// ToolProgressContent represents tool execution progress
type ToolProgressContent struct {
	CallID   string `json:"call_id"`
	TurnID   string `json:"turn_id"`
	AgentID  string `json:"agent_id,omitempty"`
	ToolName string `json:"tool_name"`

	Status   ToolStatus           `json:"status"`
	Progress *ToolProgressDetails `json:"progress,omitempty"`

	// Output preview (for long-running tools, etc.)
	OutputPreview *ToolOutputPreview `json:"output_preview,omitempty"`
}

// ToolProgressDetails contains progress information
type ToolProgressDetails struct {
	Stage   string `json:"stage,omitempty"`   // "executing", "processing", etc.
	Percent int    `json:"percent,omitempty"` // 0-100
	Message string `json:"message,omitempty"`
}

// ToolOutputPreview contains preview of tool output
type ToolOutputPreview struct {
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

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

// StreamingConfig contains streaming behavior settings
type StreamingConfig struct {
	Enabled bool `json:"enabled,omitempty"`
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
	RelInReplyTo = matrixevents.RelInReplyTo
)

// Content field keys
const (
	BeeperAIKey           = matrixevents.BeeperAIKey
	BeeperAIToolCallKey   = matrixevents.BeeperAIToolCallKey
	BeeperAIToolResultKey = matrixevents.BeeperAIToolResultKey
)

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

// ImageGenerationMetadata is added to m.image events for AI-generated images
type ImageGenerationMetadata struct {
	TurnID        string `json:"turn_id,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
	Model         string `json:"model,omitempty"`
	Style         string `json:"style,omitempty"`   // "vivid", "natural"
	Quality       string `json:"quality,omitempty"` // "standard", "hd"
}

// AttachmentMetadata describes files attached to user messages
type AttachmentMetadata struct {
	Type     string `json:"type"` // "file", "image"
	FileID   string `json:"file_id,omitempty"`
	Filename string `json:"filename,omitempty"`
	MxcURI   string `json:"mxc_uri,omitempty"`
	Mimetype string `json:"mimetype,omitempty"`
	Size     int    `json:"size,omitempty"`
	Width    int    `json:"width,omitempty"`  // For images
	Height   int    `json:"height,omitempty"` // For images
}

// AgentMemberContent is stored in m.room.member events in the Builder room
// to persist agent definitions as Matrix state events.
type AgentMemberContent struct {
	Membership  string                  `json:"membership"`
	DisplayName string                  `json:"displayname,omitempty"`
	AvatarURL   string                  `json:"avatar_url,omitempty"`
	Agent       *AgentDefinitionContent `json:"com.beeper.ai.agent,omitempty"`
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
	MemorySearch    *agents.MemorySearchConfig   `json:"memory_search,omitempty"`
	HeartbeatPrompt string                       `json:"heartbeat_prompt,omitempty"`
	CreatedAt       int64                        `json:"created_at"`
	UpdatedAt       int64                        `json:"updated_at"`
}

// MemoryFactContent stores a memory fact in a timeline event
type MemoryFactContent struct {
	FactID     string   `json:"fact_id"`
	Content    string   `json:"content"`
	Keywords   []string `json:"keywords,omitempty"`
	Category   string   `json:"category,omitempty"`    // preference, decision, entity, fact, other
	Importance float64  `json:"importance,omitempty"`  // 0-1, default 0.5
	Source     string   `json:"source,omitempty"`      // user, assistant, system
	SourceRoom string   `json:"source_room,omitempty"` // Room where the memory was created
	CreatedAt  int64    `json:"created_at"`
	UpdatedAt  int64    `json:"updated_at,omitempty"`
}

// MemoryIndexEntry represents a single entry in the memory index
type MemoryIndexEntry struct {
	FactID     string   `json:"fact_id"`
	EventID    string   `json:"event_id"`
	Keywords   []string `json:"keywords"`
	Category   string   `json:"category,omitempty"`
	Importance float64  `json:"importance"`
	Preview    string   `json:"preview"` // First 100 chars
	CreatedAt  int64    `json:"created_at"`
}
