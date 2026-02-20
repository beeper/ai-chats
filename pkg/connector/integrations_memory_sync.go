package connector

import "context"

func notifyMemoryFileChanged(ctx context.Context, path string) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil || btc.Client == nil {
		return
	}
	meta := portalMeta(btc.Portal)
	agentID := resolveAgentID(meta)
	manager, _ := btc.Client.getMemoryManager(agentID)
	if manager == nil {
		return
	}
	manager.NotifyFileChanged(path)
}
