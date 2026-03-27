package ai

import (
	"context"
	"slices"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/agents"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

type aiAgentCatalog struct {
	client    *AIClient
	connector *OpenAIConnector
}

func (c aiAgentCatalog) DefaultAgent(ctx context.Context, login *bridgev2.UserLogin) (*bridgesdk.Agent, error) {
	client := c.clientForLogin(login)
	if client == nil {
		return nil, nil
	}
	if !client.agentsEnabledForLogin() {
		return nil, nil
	}
	agent, err := NewAgentStoreAdapter(client).GetAgentByID(ctx, agents.DefaultAgentID)
	if err != nil || agent == nil {
		return nil, err
	}
	return client.sdkAgentForDefinition(ctx, agent), nil
}

func (c aiAgentCatalog) ListAgents(ctx context.Context, login *bridgev2.UserLogin) ([]*bridgesdk.Agent, error) {
	client := c.clientForLogin(login)
	if client == nil {
		return nil, nil
	}
	if !client.agentsEnabledForLogin() {
		return nil, nil
	}
	agentsMap, err := NewAgentStoreAdapter(client).LoadAgents(ctx)
	if err != nil {
		return nil, err
	}
	agentIDs := make([]string, 0, len(agentsMap))
	for agentID := range agentsMap {
		if strings.TrimSpace(agentID) != "" {
			agentIDs = append(agentIDs, agentID)
		}
	}
	slices.Sort(agentIDs)

	out := make([]*bridgesdk.Agent, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		if sdkAgent := client.sdkAgentForDefinition(ctx, agentsMap[agentID]); sdkAgent != nil {
			out = append(out, sdkAgent)
		}
	}
	return out, nil
}

func (c aiAgentCatalog) ResolveAgent(ctx context.Context, login *bridgev2.UserLogin, identifier string) (*bridgesdk.Agent, error) {
	client := c.clientForLogin(login)
	if client == nil {
		return nil, nil
	}
	if !client.agentsEnabledForLogin() {
		return nil, nil
	}
	agentID := normalizedCatalogAgentIdentifier(identifier)
	if agentID == "" {
		return nil, nil
	}
	agent, err := NewAgentStoreAdapter(client).GetAgentByID(ctx, agentID)
	if err != nil || agent == nil {
		return nil, err
	}
	return client.sdkAgentForDefinition(ctx, agent), nil
}

func (c aiAgentCatalog) clientForLogin(login *bridgev2.UserLogin) *AIClient {
	if c.client != nil {
		return c.client
	}
	if login == nil {
		return nil
	}
	return &AIClient{
		UserLogin: login,
		connector: c.connector,
	}
}

func normalizedCatalogAgentIdentifier(identifier string) string {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return ""
	}
	if agentID, ok := parseAgentFromGhostID(identifier); ok {
		return agentID
	}
	return normalizeAgentID(identifier)
}

func sdkResolveResponseForAgent(agent *bridgesdk.Agent) *bridgev2.ResolveIdentifierResponse {
	if agent == nil {
		return nil
	}
	return &bridgev2.ResolveIdentifierResponse{
		UserID:   networkid.UserID(agent.ID),
		UserInfo: agent.UserInfo(),
	}
}
