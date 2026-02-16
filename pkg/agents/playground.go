package agents

import "github.com/beeper/ai-bridge/pkg/agents/toolpolicy"

// PlaygroundAgent is a sandbox for direct model access with minimal tools.
// This is for advanced users who want raw model interaction without agent personality.
var PlaygroundAgent = &AgentDefinition{
	ID:          "playground",
	Name:        "Model Playground",
	Description: "Direct model access with minimal tools, no agent personality",
	Model: ModelConfig{
		Primary: ModelClaudeSonnet, // Default, but typically overridden by user
		Fallbacks: []string{
			ModelOpenAIGPT52,
			ModelZAIGLM47,
		},
	},
	Tools:        &toolpolicy.ToolPolicyConfig{Profile: toolpolicy.ProfileMinimal},
	PromptMode:   PromptModeNone,  // no system prompt sections
	ResponseMode: ResponseModeRaw, // no directive processing
	IsPreset:     true,
	CreatedAt:    0,
	UpdatedAt:    0,
}
