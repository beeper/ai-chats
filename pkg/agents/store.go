package agents

import (
	"context"

	"github.com/beeper/agentremote/pkg/shared/toolspec"
)

// AgentStore interface for loading and saving agents.
// Implemented by the connector to store agents in Matrix state events.
type AgentStore interface {
	// LoadAgents returns all agents for the current user.
	LoadAgents(ctx context.Context) (map[string]*AgentDefinition, error)

	// SaveAgent creates or updates an agent.
	SaveAgent(ctx context.Context, agent *AgentDefinition) error

	// DeleteAgent removes a custom agent.
	DeleteAgent(ctx context.Context, agentID string) error

	// ListModels returns available AI models.
	ListModels(ctx context.Context) ([]ModelInfo, error)

	// ListAvailableTools returns available tools.
	ListAvailableTools(ctx context.Context) ([]toolspec.ToolInfo, error)
}
