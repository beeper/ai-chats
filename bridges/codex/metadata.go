package codex

import (
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"go.mau.fi/util/jsontime"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

type UserLoginMetadata struct {
	Provider          string `json:"provider,omitempty"`
	CodexHome         string `json:"codex_home,omitempty"`
	CodexHomeManaged  bool   `json:"codex_home_managed,omitempty"`
	CodexCommand      string `json:"codex_command,omitempty"`
	CodexAuthMode     string `json:"codex_auth_mode,omitempty"`
	CodexAccountEmail string `json:"codex_account_email,omitempty"`
}

type PortalMetadata struct {
	Title         string `json:"title,omitempty"`
	Slug          string `json:"slug,omitempty"`
	IsCronRoom    bool   `json:"is_cron_room,omitempty"`
	IsCodexRoom   bool   `json:"is_codex_room,omitempty"`
	CodexThreadID string `json:"codex_thread_id,omitempty"`
	CodexCwd      string `json:"codex_cwd,omitempty"`
	ElevatedLevel string `json:"elevated_level,omitempty"`
}

type MessageMetadata struct {
	Role               string             `json:"role,omitempty"`
	Body               string             `json:"body,omitempty"`
	CompletionID       string             `json:"completion_id,omitempty"`
	FinishReason       string             `json:"finish_reason,omitempty"`
	PromptTokens       int64              `json:"prompt_tokens,omitempty"`
	CompletionTokens   int64              `json:"completion_tokens,omitempty"`
	Model              string             `json:"model,omitempty"`
	ReasoningTokens    int64              `json:"reasoning_tokens,omitempty"`
	HasToolCalls       bool               `json:"has_tool_calls,omitempty"`
	Transcript         string             `json:"transcript,omitempty"`
	TurnID             string             `json:"turn_id,omitempty"`
	AgentID            string             `json:"agent_id,omitempty"`
	ToolCalls          []ToolCallMetadata `json:"tool_calls,omitempty"`
	CanonicalSchema    string             `json:"canonical_schema,omitempty"`
	CanonicalUIMessage map[string]any     `json:"canonical_ui_message,omitempty"`
	StartedAtMs        int64              `json:"started_at_ms,omitempty"`
	FirstTokenAtMs     int64              `json:"first_token_at_ms,omitempty"`
	CompletedAtMs      int64              `json:"completed_at_ms,omitempty"`
	ThinkingContent    string             `json:"thinking_content,omitempty"`
	ThinkingTokenCount int                `json:"thinking_token_count,omitempty"`
	GeneratedFiles     []GeneratedFileRef `json:"generated_files,omitempty"`
}

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
	CallEventID   string         `json:"call_event_id,omitempty"`
	ResultEventID string         `json:"result_event_id,omitempty"`
}

type GeneratedFileRef struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
}

type GhostMetadata struct {
	LastSync jsontime.Unix `json:"last_sync,omitempty"`
}

var _ database.MetaMerger = (*MessageMetadata)(nil)

func (mm *MessageMetadata) CopyFrom(other any) {
	src, ok := other.(*MessageMetadata)
	if !ok || src == nil {
		return
	}
	if src.Role != "" {
		mm.Role = src.Role
	}
	if src.Body != "" {
		mm.Body = src.Body
	}
	if src.CompletionID != "" {
		mm.CompletionID = src.CompletionID
	}
	if src.FinishReason != "" {
		mm.FinishReason = src.FinishReason
	}
	if src.PromptTokens != 0 {
		mm.PromptTokens = src.PromptTokens
	}
	if src.CompletionTokens != 0 {
		mm.CompletionTokens = src.CompletionTokens
	}
	if src.Model != "" {
		mm.Model = src.Model
	}
	if src.ReasoningTokens != 0 {
		mm.ReasoningTokens = src.ReasoningTokens
	}
	if src.HasToolCalls {
		mm.HasToolCalls = true
	}
	if src.Transcript != "" {
		mm.Transcript = src.Transcript
	}
	if src.TurnID != "" {
		mm.TurnID = src.TurnID
	}
	if src.AgentID != "" {
		mm.AgentID = src.AgentID
	}
	if len(src.ToolCalls) > 0 {
		mm.ToolCalls = src.ToolCalls
	}
	if src.CanonicalSchema != "" {
		mm.CanonicalSchema = src.CanonicalSchema
	}
	if len(src.CanonicalUIMessage) > 0 {
		mm.CanonicalUIMessage = src.CanonicalUIMessage
	}
	if src.StartedAtMs != 0 {
		mm.StartedAtMs = src.StartedAtMs
	}
	if src.FirstTokenAtMs != 0 {
		mm.FirstTokenAtMs = src.FirstTokenAtMs
	}
	if src.CompletedAtMs != 0 {
		mm.CompletedAtMs = src.CompletedAtMs
	}
	if src.ThinkingContent != "" {
		mm.ThinkingContent = src.ThinkingContent
	}
	if src.ThinkingTokenCount != 0 {
		mm.ThinkingTokenCount = src.ThinkingTokenCount
	}
	if len(src.GeneratedFiles) > 0 {
		mm.GeneratedFiles = src.GeneratedFiles
	}
}

func loginMetadata(login *bridgev2.UserLogin) *UserLoginMetadata {
	return bridgeadapter.EnsureLoginMetadata[UserLoginMetadata](login)
}

func portalMeta(portal *bridgev2.Portal) *PortalMetadata {
	return bridgeadapter.EnsurePortalMetadata[PortalMetadata](portal)
}

func messageMeta(msg *database.Message) *MessageMetadata {
	return bridgeadapter.EnsureMessageMetadata[MessageMetadata](msg)
}

func NewTurnID() string {
	return "turn_" + strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000000"), ".", "")
}
