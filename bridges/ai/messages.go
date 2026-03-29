package ai

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
	var text string
	for _, block := range m.Blocks {
		switch block.Type {
		case PromptBlockText, PromptBlockThinking:
			if text == "" {
				text = block.Text
			} else if block.Text != "" {
				text += "\n" + block.Text
			}
		}
	}
	return text
}

// PromptContext is the bridge-local prompt envelope used throughout bridges/ai.
type PromptContext struct {
	SystemPrompt string
	Messages     []PromptMessage
	Tools        []ToolDefinition
}
