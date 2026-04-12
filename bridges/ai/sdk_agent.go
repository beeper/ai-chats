package ai

import (
	"context"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

func (oc *AIClient) sdkAgentCatalog() sdk.AgentCatalog {
	if oc == nil {
		return aiAgentCatalog{}
	}
	return aiAgentCatalog{
		client:    oc,
		connector: oc.connector,
	}
}

func (oc *AIClient) sdkAgentForDefinition(ctx context.Context, agent *agents.AgentDefinition) *sdk.Agent {
	if agent == nil {
		return nil
	}
	displayName := oc.resolveAgentDisplayName(ctx, agent)
	if displayName == "" {
		displayName = agent.Name
	}
	if displayName == "" {
		displayName = agent.ID
	}
	modelID := oc.agentDefaultModel(agent)
	if responder, err := oc.ResolveResponderForAgent(ctx, agent.ID, ResponderResolveOptions{}); err == nil && responder != nil && responder.ModelID != "" {
		modelID = responder.ModelID
	}
	return &sdk.Agent{
		ID:           string(oc.agentUserID(agent.ID)),
		Name:         displayName,
		Description:  agent.Description,
		AvatarURL:    agent.AvatarURL,
		Identifiers:  stringutil.DedupeStrings(agentContactIdentifiers(agent.ID)),
		ModelKey:     modelID,
		Capabilities: sdk.MultimodalAgentCapabilities(),
	}
}
