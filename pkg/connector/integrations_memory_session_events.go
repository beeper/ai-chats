package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

func (oc *AIClient) notifySessionMemoryChange(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	force bool,
) {
	if oc == nil || portal == nil || meta == nil {
		return
	}
	ctx = oc.backgroundContext(ctx)
	agentID := resolveAgentID(meta)
	manager, _ := oc.getMemoryManager(agentID)
	if manager == nil {
		return
	}
	manager.NotifySessionChanged(ctx, portal.PortalKey.String(), force)
}
