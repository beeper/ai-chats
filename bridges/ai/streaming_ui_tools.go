package ai

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
)

func (oc *AIClient) emitUIToolApprovalRequest(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	approvalID string,
	toolCallID string,
	toolName string,
	presentation agentremote.ApprovalPromptPresentation,
	targetEventID id.EventID,
	ttlSeconds int,
) bool {
	approvalID = strings.TrimSpace(approvalID)
	toolCallID = strings.TrimSpace(toolCallID)
	toolName = strings.TrimSpace(toolName)
	if approvalID == "" || toolCallID == "" {
		return false
	}
	if toolName == "" {
		toolName = "tool"
	}
	if portal == nil || portal.MXID == "" || oc == nil || oc.UserLogin == nil || oc.UserLogin.UserMXID == "" || oc.approvalFlow == nil {
		if oc != nil {
			log := oc.loggerForContext(ctx).Warn().
				Str("approval_id", approvalID).
				Str("tool_call_id", toolCallID)
			if portal != nil {
				log = log.Stringer("room_id", portal.MXID)
			}
			log.Msg("Skipping tool approval prompt: missing portal, owner, or approval flow context")
		}
		return false
	}

	// Emit stream event for real-time UI
	oc.toolLifecycle(portal, state).requestApproval(ctx, approvalID, toolCallID)

	turnID := ""
	if state != nil {
		turnID = state.turnID
	}
	oc.approvalFlow.SendPrompt(ctx, portal, agentremote.SendPromptParams{
		ApprovalPromptMessageParams: agentremote.ApprovalPromptMessageParams{
			ApprovalID:     approvalID,
			ToolCallID:     toolCallID,
			ToolName:       toolName,
			TurnID:         turnID,
			Presentation:   presentation,
			ReplyToEventID: targetEventID,
			ExpiresAt:      agentremote.ComputeApprovalExpiry(ttlSeconds),
		},
		RoomID:    portal.MXID,
		OwnerMXID: oc.UserLogin.UserMXID,
	})
	return true
}
