package connector

import (
	"context"
	"slices"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/tools"
)

type agentListEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Configured bool   `json:"configured"`
}

func (oc *AIClient) executeAgentsList(ctx context.Context, portal *bridgev2.Portal, _ map[string]any) (*tools.Result, error) {
	requesterAgentID := normalizeAgentID(resolveAgentID(portalMeta(portal)))
	if requesterAgentID == "" {
		requesterAgentID = normalizeAgentID(agents.DefaultAgentID)
	}

	allowAny, allowSet := oc.resolveSubagentAllowlist(ctx, requesterAgentID)

	store := NewAgentStoreAdapter(oc)
	agentsMap, err := store.LoadAgents(ctx)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load agents for agents_list")
		agentsMap = map[string]*agents.AgentDefinition{}
	}

	configured := make(map[string]*agents.AgentDefinition, len(agentsMap))
	for id, agent := range agentsMap {
		configured[normalizeAgentID(id)] = agent
	}

	allowed := make(map[string]struct{})
	if requesterAgentID != "" {
		allowed[requesterAgentID] = struct{}{}
	}
	if allowAny {
		for id := range configured {
			allowed[id] = struct{}{}
		}
	} else {
		for id := range allowSet {
			allowed[id] = struct{}{}
		}
	}

	ordered := make([]string, 0, len(allowed))
	for id := range allowed {
		if id != requesterAgentID {
			ordered = append(ordered, id)
		}
	}
	slices.Sort(ordered)
	if requesterAgentID != "" {
		ordered = append([]string{requesterAgentID}, ordered...)
	}

	entries := make([]agentListEntry, 0, len(ordered))
	for _, id := range ordered {
		entry := agentListEntry{
			ID:         id,
			Configured: false,
		}
		if agent := configured[id]; agent != nil {
			entry.Configured = true
			agentName := oc.resolveAgentDisplayName(ctx, agent)
			if agentName != "" {
				entry.Name = agentName
			}
		}
		entries = append(entries, entry)
	}

	payload := map[string]any{
		"requester": requesterAgentID,
		"allowAny":  allowAny,
		"agents":    entries,
	}
	return tools.JSONResult(payload), nil
}
