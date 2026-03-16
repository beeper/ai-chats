package agentremote

import "github.com/beeper/agentremote/pkg/shared/citations"

// BaseMessageMetadata contains fields common to all bridge MessageMetadata structs.
// Embed this in each bridge's MessageMetadata to share CopyFrom logic.
type BaseMessageMetadata struct {
	Role               string             `json:"role,omitempty"`
	Body               string             `json:"body,omitempty"`
	FinishReason       string             `json:"finish_reason,omitempty"`
	PromptTokens       int64              `json:"prompt_tokens,omitempty"`
	CompletionTokens   int64              `json:"completion_tokens,omitempty"`
	ReasoningTokens    int64              `json:"reasoning_tokens,omitempty"`
	TurnID             string             `json:"turn_id,omitempty"`
	AgentID            string             `json:"agent_id,omitempty"`
	CanonicalTurnData  map[string]any     `json:"canonical_turn_data,omitempty"`
	StartedAtMs        int64              `json:"started_at_ms,omitempty"`
	CompletedAtMs      int64              `json:"completed_at_ms,omitempty"`
	ThinkingContent    string             `json:"thinking_content,omitempty"`
	ToolCalls          []ToolCallMetadata `json:"tool_calls,omitempty"`
	GeneratedFiles     []GeneratedFileRef `json:"generated_files,omitempty"`
	ExcludeFromHistory bool               `json:"exclude_from_history,omitempty"`
}

// AssistantMessageMetadata contains fields common to assistant messages across
// bridges. Embed this in each bridge's MessageMetadata alongside BaseMessageMetadata.
type AssistantMessageMetadata struct {
	CompletionID       string `json:"completion_id,omitempty"`
	Model              string `json:"model,omitempty"`
	HasToolCalls       bool   `json:"has_tool_calls,omitempty"`
	Transcript         string `json:"transcript,omitempty"`
	FirstTokenAtMs     int64  `json:"first_token_at_ms,omitempty"`
	ThinkingTokenCount int    `json:"thinking_token_count,omitempty"`
}

// CopyFromAssistant copies non-zero assistant fields from src into the receiver.
func (a *AssistantMessageMetadata) CopyFromAssistant(src *AssistantMessageMetadata) {
	if src == nil {
		return
	}
	if src.CompletionID != "" {
		a.CompletionID = src.CompletionID
	}
	if src.Model != "" {
		a.Model = src.Model
	}
	if src.HasToolCalls {
		a.HasToolCalls = true
	}
	if src.Transcript != "" {
		a.Transcript = src.Transcript
	}
	if src.FirstTokenAtMs != 0 {
		a.FirstTokenAtMs = src.FirstTokenAtMs
	}
	if src.ThinkingTokenCount != 0 {
		a.ThinkingTokenCount = src.ThinkingTokenCount
	}
}

// CopyFromBase copies non-zero common fields from src into the receiver.
func (b *BaseMessageMetadata) CopyFromBase(src *BaseMessageMetadata) {
	if src == nil {
		return
	}
	if src.Role != "" {
		b.Role = src.Role
	}
	if src.Body != "" {
		b.Body = src.Body
	}
	if src.FinishReason != "" {
		b.FinishReason = src.FinishReason
	}
	if src.PromptTokens != 0 {
		b.PromptTokens = src.PromptTokens
	}
	if src.CompletionTokens != 0 {
		b.CompletionTokens = src.CompletionTokens
	}
	if src.ReasoningTokens != 0 {
		b.ReasoningTokens = src.ReasoningTokens
	}
	if src.TurnID != "" {
		b.TurnID = src.TurnID
	}
	if src.AgentID != "" {
		b.AgentID = src.AgentID
	}
	if len(src.CanonicalTurnData) > 0 {
		b.CanonicalTurnData = cloneJSONMap(src.CanonicalTurnData)
	}
	if src.StartedAtMs != 0 {
		b.StartedAtMs = src.StartedAtMs
	}
	if src.CompletedAtMs != 0 {
		b.CompletedAtMs = src.CompletedAtMs
	}
	if src.ThinkingContent != "" {
		b.ThinkingContent = src.ThinkingContent
	}
	if len(src.ToolCalls) > 0 {
		b.ToolCalls = make([]ToolCallMetadata, len(src.ToolCalls))
		for i, call := range src.ToolCalls {
			b.ToolCalls[i] = ToolCallMetadata{
				CallID:        call.CallID,
				ToolName:      call.ToolName,
				ToolType:      call.ToolType,
				Input:         cloneJSONMap(call.Input),
				Output:        cloneJSONMap(call.Output),
				Status:        call.Status,
				ResultStatus:  call.ResultStatus,
				ErrorMessage:  call.ErrorMessage,
				StartedAtMs:   call.StartedAtMs,
				CompletedAtMs: call.CompletedAtMs,
				CallEventID:   call.CallEventID,
				ResultEventID: call.ResultEventID,
			}
		}
	}
	if len(src.GeneratedFiles) > 0 {
		b.GeneratedFiles = make([]GeneratedFileRef, len(src.GeneratedFiles))
		copy(b.GeneratedFiles, src.GeneratedFiles)
	}
	if src.ExcludeFromHistory {
		b.ExcludeFromHistory = true
	}
}

func cloneJSONMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(src))
	for k, v := range src {
		cloned[k] = cloneJSONValue(v)
	}
	return cloned
}

func cloneJSONSlice(src []any) []any {
	if len(src) == 0 {
		return nil
	}
	cloned := make([]any, len(src))
	for i, v := range src {
		cloned[i] = cloneJSONValue(v)
	}
	return cloned
}

func cloneJSONValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return cloneJSONMap(typed)
	case []any:
		return cloneJSONSlice(typed)
	default:
		return v
	}
}

// ToolCallMetadata tracks a tool call within a message.
// Both bridges and the connector share this type for JSON-serialized database storage.
type ToolCallMetadata struct {
	CallID        string         `json:"call_id"`
	ToolName      string         `json:"tool_name"`
	ToolType      string         `json:"tool_type"`
	Input         map[string]any `json:"input,omitempty"`
	Output        map[string]any `json:"output,omitempty"`
	Status        string         `json:"status"`
	ResultStatus  string         `json:"result_status,omitempty"`
	ErrorMessage  string         `json:"error_message,omitempty"`
	StartedAtMs   int64          `json:"started_at_ms,omitempty"`
	CompletedAtMs int64          `json:"completed_at_ms,omitempty"`

	// Event IDs for timeline events (if emitted as separate events)
	CallEventID   string `json:"call_event_id,omitempty"`
	ResultEventID string `json:"result_event_id,omitempty"`
}

// GeneratedFileRef stores a reference to a file generated by the assistant (e.g., image generation).
type GeneratedFileRef struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
}

// GeneratedFileRefsFromParts converts citations.GeneratedFilePart values into
// GeneratedFileRef values suitable for message metadata storage.
func GeneratedFileRefsFromParts(parts []citations.GeneratedFilePart) []GeneratedFileRef {
	if len(parts) == 0 {
		return nil
	}
	refs := make([]GeneratedFileRef, len(parts))
	for i, f := range parts {
		refs[i] = GeneratedFileRef{URL: f.URL, MimeType: f.MediaType}
	}
	return refs
}

// AssistantMetadataParams holds the bridge-agnostic fields needed to populate
// an assistant message's BaseMessageMetadata. Each bridge extracts these from
// its own streamingState type and passes them here.
type AssistantMetadataParams struct {
	Body              string
	FinishReason      string
	TurnID            string
	AgentID           string
	StartedAtMs       int64
	CompletedAtMs     int64
	ThinkingContent   string
	PromptTokens      int64
	CompletionTokens  int64
	ReasoningTokens   int64
	ToolCalls         []ToolCallMetadata
	GeneratedFiles    []GeneratedFileRef
	CanonicalTurnData map[string]any
}

// BuildAssistantBaseMetadata constructs a BaseMessageMetadata for an assistant
// message from the given params. This deduplicates the common field-population
// logic shared across bridge saveAssistantMessage implementations.
func BuildAssistantBaseMetadata(p AssistantMetadataParams) BaseMessageMetadata {
	return BaseMessageMetadata{
		Role:              "assistant",
		Body:              p.Body,
		FinishReason:      p.FinishReason,
		TurnID:            p.TurnID,
		AgentID:           p.AgentID,
		ToolCalls:         p.ToolCalls,
		StartedAtMs:       p.StartedAtMs,
		CompletedAtMs:     p.CompletedAtMs,
		GeneratedFiles:    p.GeneratedFiles,
		ThinkingContent:   p.ThinkingContent,
		PromptTokens:      p.PromptTokens,
		CompletionTokens:  p.CompletionTokens,
		ReasoningTokens:   p.ReasoningTokens,
		CanonicalTurnData: p.CanonicalTurnData,
	}
}
