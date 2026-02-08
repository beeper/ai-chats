package opencodebridge

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/matrixevents"
)

const (
	BeeperAIToolCallKey   = matrixevents.BeeperAIToolCallKey
	BeeperAIToolResultKey = matrixevents.BeeperAIToolResultKey
)

// ToolStatus represents the state of a tool call.
type ToolStatus string

const (
	ToolStatusPending   ToolStatus = "pending"
	ToolStatusRunning   ToolStatus = "running"
	ToolStatusCompleted ToolStatus = "completed"
	ToolStatusFailed    ToolStatus = "failed"
)

// ResultStatus represents the status of a tool result.
type ResultStatus string

const (
	ResultStatusSuccess ResultStatus = "success"
	ResultStatusError   ResultStatus = "error"
)

// ToolType identifies the category of tool.
type ToolType string

const (
	ToolTypeBuiltin ToolType = "builtin"
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

func firstNonEmptyString(values ...any) string {
	for _, raw := range values {
		switch v := raw.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func toolDisplayTitle(toolName string) string {
	toolName = strings.TrimSpace(toolName)
	switch toolName {
	case "web_search", "better_web_search":
		return "Web Search"
	case "image_generation", "image_generate":
		return "Image Generation"
	default:
		return toolName
	}
}
