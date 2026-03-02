package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

func (oc *AIClient) resolveQueueSettingsForPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	inlineMode airuntime.QueueMode,
	inlineOpts airuntime.QueueInlineOptions,
) (airuntime.QueueSettings, *sessionEntry, sessionStoreRef, string) {
	agentID := normalizeAgentID(resolveAgentID(meta))
	storeRef := oc.resolveSessionStoreRef(agentID)
	sessionKey := ""
	var entry *sessionEntry
	if portal != nil && portal.MXID != "" {
		sessionKey = portal.MXID.String()
		if stored, ok := oc.getSessionEntry(ctx, storeRef, sessionKey); ok {
			entry = &stored
		}
	}
	var cfg *Config
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	settings := resolveQueueSettings(queueResolveParams{
		cfg:        cfg,
		channel:    "matrix",
		session:    entry,
		inlineMode: inlineMode,
		inlineOpts: inlineOpts,
	})
	return settings, entry, storeRef, sessionKey
}
