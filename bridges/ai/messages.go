package ai

import "strings"

type PromptRole string

const (
	PromptRoleUser       PromptRole = "user"
	PromptRoleAssistant  PromptRole = "assistant"
	PromptRoleToolResult PromptRole = "tool_result"
)

type PromptBlockType string

const (
	PromptBlockText     PromptBlockType = "text"
	PromptBlockImage    PromptBlockType = "image"
	PromptBlockThinking PromptBlockType = "thinking"
	PromptBlockToolCall PromptBlockType = "tool_call"
)

type PromptBlock struct {
	Type PromptBlockType

	Text string

	ImageURL string
	ImageB64 string
	MimeType string

	ToolCallID        string
	ToolName          string
	ToolCallArguments string
}

type PromptMessage struct {
	Role       PromptRole
	Blocks     []PromptBlock
	ToolCallID string
	ToolName   string
	IsError    bool
}

func (m PromptMessage) Text() string {
	var sb strings.Builder
	for _, block := range m.Blocks {
		switch block.Type {
		case PromptBlockText, PromptBlockThinking:
			if block.Text != "" {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(block.Text)
			}
		}
	}
	return sb.String()
}

// PromptContext is the bridge-local prompt envelope used throughout bridges/ai.
type PromptContext struct {
	SystemPrompt string
	Messages     []PromptMessage
	Tools        []ToolDefinition
}
