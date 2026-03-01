package connector

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

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
