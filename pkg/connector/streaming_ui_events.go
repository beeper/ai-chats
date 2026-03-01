package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
)

func (oc *AIClient) emitUIRuntimeMetadata(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
	extra map[string]any,
) {
	base := oc.buildUIMessageMetadata(state, meta, false)
	if len(extra) > 0 {
		base = mergeMaps(base, extra)
	}
	oc.uiEmitter(state).EmitUIMessageMetadata(ctx, portal, base)
}

func (oc *AIClient) emitUIStart(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	oc.uiEmitter(state).EmitUIStart(ctx, portal, oc.buildUIMessageMetadata(state, meta, false))
}
