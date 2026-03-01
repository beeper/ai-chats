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
	oc.emitUIMessageMetadata(ctx, portal, state, base)
}

func (oc *AIClient) emitUIStart(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	oc.uiEmitter(state).EmitUIStart(ctx, portal, oc.buildUIMessageMetadata(state, meta, false))
}

func (oc *AIClient) emitUIMessageMetadata(ctx context.Context, portal *bridgev2.Portal, state *streamingState, metadata map[string]any) {
	oc.uiEmitter(state).EmitUIMessageMetadata(ctx, portal, metadata)
}

func (oc *AIClient) emitUIAbort(ctx context.Context, portal *bridgev2.Portal, state *streamingState, reason string) {
	oc.uiEmitter(state).EmitUIAbort(ctx, portal, reason)
}

func (oc *AIClient) emitUIStepStart(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	oc.uiEmitter(state).EmitUIStepStart(ctx, portal)
}

func (oc *AIClient) emitUIStepFinish(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	oc.uiEmitter(state).EmitUIStepFinish(ctx, portal)
}

func (oc *AIClient) ensureUIText(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	oc.uiEmitter(state).EnsureUIText(ctx, portal)
}

func (oc *AIClient) ensureUIReasoning(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	oc.uiEmitter(state).EnsureUIReasoning(ctx, portal)
}

func (oc *AIClient) emitUITextDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, delta string) {
	oc.uiEmitter(state).EmitUITextDelta(ctx, portal, delta)
}

func (oc *AIClient) emitUIReasoningDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, delta string) {
	oc.uiEmitter(state).EmitUIReasoningDelta(ctx, portal, delta)
}
