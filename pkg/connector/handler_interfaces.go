package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

// HandleMatrixActionResponse handles com.beeper.action_response events (MSC1485 action hints).
// This implements bridgev2.ActionResponseHandlingNetworkAPI.
func (oc *AIClient) HandleMatrixActionResponse(ctx context.Context, msg *bridgev2.MatrixActionResponse) error {
	if msg == nil || msg.Event == nil || msg.Content == nil || msg.Portal == nil {
		return nil
	}
	logCtx := oc.loggerForContext(ctx)

	parsed := bridgeadapter.ParseActionResponse(msg.Content)
	if parsed == nil {
		logCtx.Warn().Msg("action response: failed to parse payload")
		return nil
	}

	approve, always, ok := bridgeadapter.ActionDecisionFromString(parsed.ActionID)
	if !ok {
		logCtx.Warn().Str("action_id", parsed.ActionID).Msg("action response: unknown action_id")
		return nil
	}

	approvalID := strings.TrimSpace(parsed.ApprovalID)
	if approvalID == "" {
		logCtx.Warn().Msg("action response: missing approval_id in context")
		return nil
	}

	state := airuntime.ToolApprovalDenied
	if approve {
		state = airuntime.ToolApprovalApproved
	}
	err := oc.resolveToolApproval(
		msg.Portal.MXID,
		approvalID,
		airuntime.ToolApprovalDecision{
			State: state,
		},
		always,
		msg.Event.Sender,
	)
	if err != nil {
		logCtx.Warn().Err(err).Str("approval_id", approvalID).Msg("action response: failed to resolve approval")
		oc.sendApprovalRejectionEvent(ctx, msg.Portal, approvalID, err)
	}
	return nil
}

// HandleMatrixReadReceipt tracks read receipt positions. AI-bridge is the
// authoritative side so there is nothing to forward to a remote network.
func (oc *AIClient) HandleMatrixReadReceipt(ctx context.Context, msg *bridgev2.MatrixReadReceipt) error {
	return nil
}

// HandleMatrixRoomName handles room rename events from Matrix.
// Returns true to indicate the name change was accepted (no remote to forward to).
func (oc *AIClient) HandleMatrixRoomName(ctx context.Context, msg *bridgev2.MatrixRoomName) (bool, error) {
	return true, nil
}

// HandleMatrixRoomTopic handles room topic change events from Matrix.
// Returns true to indicate the topic change was accepted.
func (oc *AIClient) HandleMatrixRoomTopic(ctx context.Context, msg *bridgev2.MatrixRoomTopic) (bool, error) {
	return true, nil
}

// HandleMatrixRoomAvatar handles room avatar change events from Matrix.
// Returns true to indicate the avatar change was accepted.
func (oc *AIClient) HandleMatrixRoomAvatar(ctx context.Context, msg *bridgev2.MatrixRoomAvatar) (bool, error) {
	return true, nil
}

// HandleMute tracks mute state for portals. No remote forwarding needed.
func (oc *AIClient) HandleMute(ctx context.Context, msg *bridgev2.MatrixMute) error {
	return nil
}

// HandleMarkedUnread tracks unread state for portals. No remote forwarding needed.
func (oc *AIClient) HandleMarkedUnread(ctx context.Context, msg *bridgev2.MatrixMarkedUnread) error {
	return nil
}
