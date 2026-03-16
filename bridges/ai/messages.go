package ai

import bridgesdk "github.com/beeper/agentremote/sdk"

type PromptRole = bridgesdk.PromptRole

const (
	PromptRoleUser       PromptRole = bridgesdk.PromptRoleUser
	PromptRoleAssistant  PromptRole = bridgesdk.PromptRoleAssistant
	PromptRoleToolResult PromptRole = bridgesdk.PromptRoleToolResult
)

type PromptBlockType = bridgesdk.PromptBlockType

const (
	PromptBlockText     PromptBlockType = bridgesdk.PromptBlockText
	PromptBlockImage    PromptBlockType = bridgesdk.PromptBlockImage
	PromptBlockFile     PromptBlockType = bridgesdk.PromptBlockFile
	PromptBlockThinking PromptBlockType = bridgesdk.PromptBlockThinking
	PromptBlockToolCall PromptBlockType = bridgesdk.PromptBlockToolCall
	PromptBlockAudio    PromptBlockType = bridgesdk.PromptBlockAudio
	PromptBlockVideo    PromptBlockType = bridgesdk.PromptBlockVideo
)

type PromptBlock = bridgesdk.PromptBlock
type PromptMessage = bridgesdk.PromptMessage

// PromptContext extends the shared provider-facing prompt model with bridge-local tool definitions.
type PromptContext struct {
	bridgesdk.PromptContext
	Tools []ToolDefinition
}
