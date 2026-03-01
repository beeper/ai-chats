package connector

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) ensureUIToolInputStart(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	toolCallID string,
	toolName string,
	providerExecuted bool,
	dynamic bool,
	title string,
	providerMetadata map[string]any,
) {
	oc.uiEmitter(state).EnsureUIToolInputStart(ctx, portal, toolCallID, toolName, providerExecuted, dynamic, title, providerMetadata)
}

func (oc *AIClient) emitUIToolInputDelta(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, toolName, delta string, providerExecuted bool) {
	oc.uiEmitter(state).EmitUIToolInputDelta(ctx, portal, toolCallID, toolName, delta, providerExecuted)
}

func (oc *AIClient) emitUIToolInputAvailable(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, toolName string, input any, providerExecuted bool) {
	oc.uiEmitter(state).EmitUIToolInputAvailable(ctx, portal, toolCallID, toolName, input, providerExecuted)
}

func (oc *AIClient) emitUIToolInputError(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	toolCallID, toolName string,
	input any,
	errorText string,
	providerExecuted bool,
	dynamic bool,
) {
	oc.uiEmitter(state).EmitUIToolInputError(ctx, portal, toolCallID, toolName, input, errorText, providerExecuted, dynamic)
}

func (oc *AIClient) emitUIToolApprovalRequest(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	approvalID string,
	toolCallID string,
	toolName string,
	targetEventID id.EventID,
	ttlSeconds int,
) {
	oc.uiEmitter(state).EmitUIToolApprovalRequest(ctx, portal, approvalID, toolCallID, toolName, ttlSeconds)

	// Send a second tool_call timeline event with approval data so the desktop
	// ToolEventGrouper can render inline approval buttons.
	approvalExpiresAtMs := int64(0)
	if ttlSeconds > 0 {
		approvalExpiresAtMs = time.Now().Add(time.Duration(ttlSeconds) * time.Second).UnixMilli()
	}
	oc.sendToolCallApprovalEvent(ctx, portal, state, toolCallID, toolName, approvalID, approvalExpiresAtMs)
}

func (oc *AIClient) emitUIToolOutputAvailable(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID string, output any, providerExecuted bool, preliminary bool) {
	oc.uiEmitter(state).EmitUIToolOutputAvailable(ctx, portal, toolCallID, output, providerExecuted, preliminary)
}

func (oc *AIClient) emitUIToolOutputDenied(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID string) {
	oc.uiEmitter(state).EmitUIToolOutputDenied(ctx, portal, toolCallID)
}

func (oc *AIClient) emitUIToolOutputError(ctx context.Context, portal *bridgev2.Portal, state *streamingState, toolCallID, errorText string, providerExecuted bool) {
	oc.uiEmitter(state).EmitUIToolOutputError(ctx, portal, toolCallID, errorText, providerExecuted)
}
