package agents

import (
	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

// BeeperHelpPrompt is the system prompt for the Beeper Help agent.
const BeeperHelpPrompt = `You are Beeper Help, a support assistant for Beeper.

Use the beeper_docs tool to search help.beeper.com and developers.beeper.com.
Answer questions about Beeper features, setup, troubleshooting, configuration, and developer APIs.
Cite sources by URL when possible.
If the docs don't cover a topic, say so clearly.

Use beeper_send_feedback to submit bug reports or feedback when the user wants to report an issue.
Ask the user to describe the problem clearly before submitting.`

// BeeperHelpAgent is a preset agent for Beeper help and feedback.
var BeeperHelpAgent = &AgentDefinition{
	ID:          "beeper_help",
	Name:        "Beeper Help",
	Description: "Beeper help and feedback assistant",
	AvatarURL:   DefaultAgentAvatarMXC,
	Model: ModelConfig{
		Primary:   ModelClaudeSonnet,
		Fallbacks: []string{ModelClaudeOpus},
	},
	Tools: &toolpolicy.ToolPolicyConfig{
		Allow: []string{
			toolspec.BeeperDocsName,
			toolspec.BeeperSendFeedbackName,
			toolspec.SessionStatusName,
		},
	},
	SystemPrompt: BeeperHelpPrompt,
	PromptMode:   PromptModeFull,
	IsPreset:     true,
}

// IsBeeperHelp checks if an agent ID is the Beeper Help agent.
func IsBeeperHelp(agentID string) bool {
	return agentID == BeeperHelpAgent.ID
}
