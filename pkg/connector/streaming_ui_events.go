package connector

import (
	"context"
	"fmt"
	"strings"

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
	if state.uiStarted {
		return
	}
	state.uiStarted = true
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":            "start",
		"messageId":       state.turnID,
		"messageMetadata": oc.buildUIMessageMetadata(state, meta, false),
	})
}

func (oc *AIClient) emitUIMessageMetadata(ctx context.Context, portal *bridgev2.Portal, state *streamingState, metadata map[string]any) {
	if len(metadata) == 0 {
		return
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":            "message-metadata",
		"messageMetadata": metadata,
	})
}

func (oc *AIClient) emitUIAbort(ctx context.Context, portal *bridgev2.Portal, state *streamingState, reason string) {
	part := map[string]any{
		"type": "abort",
	}
	if strings.TrimSpace(reason) != "" {
		part["reason"] = reason
	}
	oc.emitStreamEvent(ctx, portal, state, part)
}

func (oc *AIClient) emitUIStepStart(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if state.uiStepOpen {
		return
	}
	state.uiStepOpen = true
	state.uiStepCount++
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type": "start-step",
	})
}

func (oc *AIClient) emitUIStepFinish(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if !state.uiStepOpen {
		return
	}
	state.uiStepOpen = false
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type": "finish-step",
	})
}

func (oc *AIClient) ensureUIText(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if state.uiTextID != "" {
		return
	}
	state.uiTextID = fmt.Sprintf("text-%s", state.turnID)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type": "text-start",
		"id":   state.uiTextID,
	})
}

func (oc *AIClient) ensureUIReasoning(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if state.uiReasoningID != "" {
		return
	}
	state.uiReasoningID = fmt.Sprintf("reasoning-%s", state.turnID)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type": "reasoning-start",
		"id":   state.uiReasoningID,
	})
}

func (oc *AIClient) emitUITextDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, delta string) {
	oc.ensureUIText(ctx, portal, state)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":  "text-delta",
		"id":    state.uiTextID,
		"delta": delta,
	})
}

func (oc *AIClient) emitUIReasoningDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, delta string) {
	oc.ensureUIReasoning(ctx, portal, state)
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":  "reasoning-delta",
		"id":    state.uiReasoningID,
		"delta": delta,
	})
}
