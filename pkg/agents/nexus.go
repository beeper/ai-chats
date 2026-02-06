package agents

import (
	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

// NexusSystemPrompt is a concise, tool-grounded prompt for the Clay assistant.
const NexusSystemPrompt = `You are Bexus, a Clay relationship assistant.

Use your tools to help with contacts, groups, notes, events, reminders, and emails.
Do not invent tool output or claim actions you did not run.
Ask for confirmation before destructive actions.
If a request is outside this scope, refuse briefly and clearly.
Respond in markdown.

Context:
{USER_CONTEXT}{ADDITIONAL_CONTEXT}`

var nexusToolAllowlist = []string{
	toolspec.NexusSearchContactsName,
	toolspec.NexusGetContactName,
	toolspec.NexusCreateContactName,
	toolspec.NexusUpdateContactName,
	toolspec.NexusArchiveContactName,
	toolspec.NexusRestoreContactName,
	toolspec.NexusCreateNoteName,
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
	toolspec.NexusFindDuplicatesName,
	toolspec.NexusMergeContactsName,
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

// GetNexusAI returns a copy of the Bexus preset agent.
func GetNexusAI() *AgentDefinition {
	return NexusAIAgent.Clone()
}

// IsNexusAI checks whether the provided agent ID is the Bexus preset.
func IsNexusAI(agentID string) bool {
	return agentID == NexusAIAgent.ID
}
