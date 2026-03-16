package ai

import (
	"context"
	"strings"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/textfs"
)

func maybeRefreshAgentIdentity(ctx context.Context, rawPath string) {
	if ctx == nil || strings.TrimSpace(rawPath) == "" {
		return
	}
	normalized, err := textfs.NormalizePath(rawPath)
	if err != nil {
		return
	}
	if !strings.EqualFold(normalized, agents.DefaultIdentityFilename) {
		return
	}
	btc := GetBridgeToolContext(ctx)
	if btc == nil || btc.Client == nil || btc.Portal == nil {
		return
	}
	meta := btc.Meta
	if meta == nil && btc.Portal.Metadata != nil {
		meta = portalMeta(btc.Portal)
	}
	agentID := resolveAgentID(meta)
	if agentID == "" {
		return
	}
	store := NewAgentStoreAdapter(btc.Client)
	agent, err := store.GetAgentByID(ctx, agentID)
	if err != nil || agent == nil {
		return
	}
	modelID := btc.Client.effectiveModel(meta)
	agentName := btc.Client.resolveAgentDisplayName(ctx, agent)
	btc.Client.ensureAgentGhostDisplayName(ctx, agent.ID, modelID, agentName)
}
