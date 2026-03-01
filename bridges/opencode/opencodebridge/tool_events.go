package opencodebridge

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/agents/tools"
	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

type ToolStatus = matrixevents.ToolStatus

const (
	ToolStatusPending   = matrixevents.ToolStatusPending
	ToolStatusRunning   = matrixevents.ToolStatusRunning
	ToolStatusCompleted = matrixevents.ToolStatusCompleted
	ToolStatusFailed    = matrixevents.ToolStatusFailed
)

type ResultStatus = matrixevents.ResultStatus

const (
	ResultStatusSuccess = matrixevents.ResultStatusSuccess
	ResultStatusError   = matrixevents.ResultStatusError
)

type ToolType = matrixevents.ToolType

const (
	ToolTypeBuiltin = matrixevents.ToolTypeBuiltin
)

// TimingInfo contains timing information for events.
type TimingInfo struct {
	StartedAt    int64 `json:"started_at,omitempty"`
	FirstTokenAt int64 `json:"first_token_at,omitempty"`
	CompletedAt  int64 `json:"completed_at,omitempty"`
}

// ToolCallData contains the tool call metadata.
type ToolCallData struct {
	CallID   string         `json:"call_id"`
	ToolName string         `json:"tool_name"`
	ToolType ToolType       `json:"tool_type"`
	Status   ToolStatus     `json:"status"`
	Input    map[string]any `json:"input,omitempty"`
	Display  *ToolDisplay   `json:"display,omitempty"`
	Timing   *TimingInfo    `json:"timing,omitempty"`
}

// ToolDisplay contains display hints for tool rendering.
type ToolDisplay struct {
	Title     string `json:"title,omitempty"`
	Icon      string `json:"icon,omitempty"`
	Collapsed bool   `json:"collapsed,omitempty"`
}

// ToolResultData contains the tool result metadata.
type ToolResultData struct {
	CallID   string             `json:"call_id"`
	ToolName string             `json:"tool_name"`
	Status   ResultStatus       `json:"status"`
	Output   map[string]any     `json:"output,omitempty"`
	Display  *ToolResultDisplay `json:"display,omitempty"`
}

// ToolResultDisplay contains display hints for tool result rendering.
type ToolResultDisplay struct {
	Format          string `json:"format,omitempty"`
	Expandable      bool   `json:"expandable,omitempty"`
	DefaultExpanded bool   `json:"default_expanded,omitempty"`
	ShowStdout      bool   `json:"show_stdout,omitempty"`
	ShowArtifacts   bool   `json:"show_artifacts,omitempty"`
}

func firstNonEmptyString(values ...string) string {
	return stringutil.FirstNonEmpty(values...)
}

func toolDisplayTitle(toolName string) string {
	toolName = strings.TrimSpace(toolName)
	if t := tools.GetTool(toolName); t != nil && t.Annotations != nil && t.Annotations.Title != "" {
		return t.Annotations.Title
	}
	return toolName
}
