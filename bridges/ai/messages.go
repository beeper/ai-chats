package ai

import (
	"strings"

	"github.com/beeper/ai-chats/sdk"
)

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

func (m PromptMessage) text(includeThinking bool) string {
	var sb strings.Builder
	for _, block := range m.Blocks {
		switch block.Type {
		case PromptBlockText:
			if block.Text != "" {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(block.Text)
			}
		case PromptBlockThinking:
			if !includeThinking || block.Text == "" {
				continue
			}
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

func (m PromptMessage) Text() string {
	return m.text(true)
}

func (m PromptMessage) VisibleText() string {
	return m.text(false)
}

// PromptContext is the bridge-local prompt envelope used throughout bridges/ai.
type PromptContext struct {
	SystemPrompt    string
	Messages        []PromptMessage
	Tools           []ToolDefinition
	CurrentTurnData sdk.TurnData
}

func promptCurrentUserVisibleText(prompt PromptContext) string {
	if prompt.CurrentTurnData.Role != "" || prompt.CurrentTurnData.ID != "" || len(prompt.CurrentTurnData.Parts) > 0 {
		return turnDataVisibleText(prompt.CurrentTurnData)
	}
	for i := len(prompt.Messages) - 1; i >= 0; i-- {
		if prompt.Messages[i].Role != PromptRoleUser {
			continue
		}
		text := strings.TrimSpace(prompt.Messages[i].VisibleText())
		if text != "" {
			return text
		}
	}
	return ""
}

func turnDataVisibleText(turn sdk.TurnData) string {
	var parts []string
	for _, part := range turn.Parts {
		if part.Type == "text" {
			text := strings.TrimSpace(part.Text)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	return ""
}
