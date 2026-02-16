package agents

import (
	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

// BeeperSearchPrompt is the system prompt for the Beeper Search agent.
const BeeperSearchPrompt = `You are Beeper Search, a focused research assistant.

## Core Behavior
- Prioritize up-to-date, source-backed answers.
- Use web search tools for current or uncertain facts.
- Be concise and clearly summarize findings.
- When possible, reference sources by name/domain.`

// BeeperSearchAgent is a preset agent optimized for web research.
var BeeperSearchAgent = &AgentDefinition{
	ID:          "beeper_search",
	Name:        "Beeper Search",
	Description: "Research assistant with web search enabled",
	Model: ModelConfig{
		Primary: ModelOpenAIGPT52,
		Fallbacks: []string{
			ModelClaudeSonnet,
			ModelClaudeOpus,
		},
	},
	Tools: &toolpolicy.ToolPolicyConfig{
		Profile: toolpolicy.ProfileFull,
		Deny:    []string{toolspec.WebSearchName},
	},
	SystemPrompt: BeeperSearchPrompt,
	PromptMode:   PromptModeFull,
	IsPreset:     true,
	CreatedAt:    0,
	UpdatedAt:    0,
}
