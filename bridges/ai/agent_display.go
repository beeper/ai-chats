package ai

import (
	"context"
	"strings"

	"github.com/beeper/agentremote/pkg/agents"
)

func (oc *AIClient) resolveAgentDisplayName(ctx context.Context, agent *agents.AgentDefinition) string {
	if agent == nil {
		return ""
	}
	name := strings.TrimSpace(agent.EffectiveName())
	if name == "" {
		return ""
	}
	if name == agent.Name {
		if identityName := oc.resolveAgentIdentityName(ctx, agent.ID); identityName != "" {
			return identityName
		}
	}
	return name
}

func (oc *AIClient) resolveAgentIdentityName(ctx context.Context, agentID string) string {
	if agentID == "" || oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	store, err := oc.textFSStoreForAgent(agentID)
	if err != nil {
		return ""
	}
	entry, found, err := store.Read(ctx, agents.DefaultIdentityFilename)
	if err != nil || !found || entry == nil {
		return ""
	}
	identity := agents.ParseIdentityMarkdown(entry.Content)
	return strings.TrimSpace(identity.Name)
}

func (oc *AIClient) agentDefaultModel(agent *agents.AgentDefinition) string {
	if agent == nil {
		return oc.effectiveModel(nil)
	}
	if agent.Model.Primary != "" {
		return ResolveAlias(agent.Model.Primary)
	}
	return oc.effectiveModel(nil)
}
