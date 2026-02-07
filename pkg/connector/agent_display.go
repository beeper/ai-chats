package connector

import (
	"context"
	"strings"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

func (oc *AIClient) resolveAgentDisplayName(ctx context.Context, agent *agents.AgentDefinition) string {
	if agent == nil {
		return ""
	}
	name := strings.TrimSpace(agent.EffectiveName())
	if name == "" {
		name = strings.TrimSpace(agent.Name)
	}
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
	store := textfs.NewStore(
		oc.UserLogin.Bridge.DB.Database,
		string(oc.UserLogin.Bridge.DB.BridgeID),
		string(oc.UserLogin.ID),
		agentID,
	)
	entry, found, err := store.Read(ctx, agents.DefaultIdentityFilename)
	if err != nil || !found || entry == nil {
		return ""
	}
	identity := agents.ParseIdentityMarkdown(entry.Content)
	return strings.TrimSpace(identity.Name)
}

// agentDefaultModel returns the default model for an agent.
func (oc *AIClient) agentDefaultModel(agent *agents.AgentDefinition) string {
	if agent == nil {
		return oc.effectiveModel(nil)
	}
	if override := oc.agentModelOverride(agent.ID); override != "" {
		return ResolveAlias(override)
	}
	if agent.Model.Primary != "" {
		return ResolveAlias(agent.Model.Primary)
	}
	return oc.effectiveModel(nil)
}
