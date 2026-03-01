package agents

import "github.com/beeper/ai-bridge/pkg/agents/toolpolicy"

// SimpleAgent provides direct model access without agent features.
var SimpleAgent = &AgentDefinition{
	ID:          "simple",
	Name:        "Simple Mode",
	Description: "Direct model access without agent features",
	Model: ModelConfig{
		Primary: ModelClaudeSonnet, // Default, but typically overridden by user
		Fallbacks: []string{
			ModelOpenAIGPT52,
			ModelZAIGLM47,
		},
	},
	Tools:        &toolpolicy.ToolPolicyConfig{Profile: toolpolicy.ProfileSimple},
	PromptMode:   PromptModeNone,     // no system prompt sections
	ResponseMode: ResponseModeSimple, // no directive processing
	IsPreset:     true,
	CreatedAt:    0,
	UpdatedAt:    0,
}
