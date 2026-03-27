package agents

import "slices"

// Model constants for preset agents (aligned with clawdbot recommended models).
const (
	ModelClaudeSonnet = "anthropic/claude-sonnet-4.5"
	ModelClaudeOpus   = "anthropic/claude-opus-4.6"
	ModelOpenAIGPT52  = "openai/gpt-5.2"
	ModelZAIGLM47     = "z-ai/glm-4.7"
)

// PresetAgents contains the default agent definitions:
// Beeper AI (default), Beeper Search, and Beeper Help.
var PresetAgents = []*AgentDefinition{
	BeeperAIAgent,
	BeeperSearchAgent,
	BeeperHelpAgent,
}

// GetPresetByID returns a preset agent by ID.
func GetPresetByID(id string) *AgentDefinition {
	for _, preset := range PresetAgents {
		if preset.ID == id {
			return preset.Clone()
		}
	}
	return nil
}

// IsPreset checks if an agent ID corresponds to a preset agent.
func IsPreset(agentID string) bool {
	return slices.ContainsFunc(PresetAgents, func(a *AgentDefinition) bool {
		return a.ID == agentID
	})
}
