package ai

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func (oc *AIClient) resolveQueueSettingsForPortal(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	inlineMode airuntime.QueueMode,
	inlineOpts airuntime.QueueInlineOptions,
) airuntime.QueueSettings {
	var cfg *Config
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	settings := resolveQueueSettings(queueResolveParams{
		cfg:        cfg,
		channel:    "matrix",
		inlineMode: inlineMode,
		inlineOpts: inlineOpts,
	})
	return settings
}
