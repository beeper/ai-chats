package agents

import (
	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

// NexusSystemPrompt is a concise, tool-grounded prompt for the Clay assistant.
const NexusSystemPrompt = `You are Bexus, a Clay relationship assistant.

Use your tools to help with contacts, groups, notes, events, reminders, and emails.
Prefer the compact contacts tool (contacts) for contact operations (search/get/create/update/note/duplicates).
Do not invent tool output or claim actions you did not run.
Ask for confirmation before destructive actions.
If a request is outside this scope, refuse briefly and clearly.
Respond in markdown.

Context:
{USER_CONTEXT}{ADDITIONAL_CONTEXT}`

var nexusToolAllowlist = []string{
	toolspec.NexusContactsName,
	toolspec.NexusGetGroupsName,
	toolspec.NexusCreateGroupName,
	toolspec.NexusUpdateGroupName,
	toolspec.NexusGetNotesName,
	toolspec.NexusGetEventsName,
	toolspec.NexusGetUpcomingEventsName,
	toolspec.NexusGetEmailsName,
	toolspec.NexusGetRecentEmailsName,
	toolspec.NexusGetRecentRemindersName,
	toolspec.NexusGetUpcomingRemindersName,
}

// NexusAIAgent is a preset agent configured for Clay relationship workflows.
var NexusAIAgent = &AgentDefinition{
	ID:          "nexus",
	Name:        "Bexus",
	Description: "Clay relationship assistant",
	AvatarURL:   DefaultAgentAvatarMXC,
	Model: ModelConfig{
		Primary: ModelOpenAIGPT52,
		Fallbacks: []string{
			ModelClaudeSonnet,
			ModelClaudeOpus,
			ModelZAIGLM47,
		},
	},
	Tools: &toolpolicy.ToolPolicyConfig{
		Allow: append([]string{}, nexusToolAllowlist...),
	},
	SystemPrompt: NexusSystemPrompt,
	PromptMode:   PromptModeFull,
	IsPreset:     true,
	CreatedAt:    0,
	UpdatedAt:    0,
}

// IsNexusAI checks whether the provided agent ID is the Bexus preset.
func IsNexusAI(agentID string) bool {
	return agentID == NexusAIAgent.ID
}
