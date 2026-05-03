package aihelpers

import (
	"context"

	"github.com/google/uuid"
)

type sdkApprovalHandle struct {
	approvalID string
	toolCallID string
	turn       *Turn
}

func (h *sdkApprovalHandle) ID() string {
	if h == nil {
		return ""
	}
	return h.approvalID
}
func (h *sdkApprovalHandle) ToolCallID() string {
	if h == nil {
		return ""
	}
	return h.toolCallID
}
func (h *sdkApprovalHandle) Wait(ctx context.Context) (ToolApprovalResponse, error) {
	if h == nil || h.turn == nil || h.turn.conv == nil || h.turn.turnCtx == nil {
		return ToolApprovalResponse{}, nil
	}
	return WaitToolApprovalHandle(ctx, WaitToolApprovalHandleParams{
		Turn:       h.turn,
		ApprovalID: h.approvalID,
		ToolCallID: h.toolCallID,
	}, func(ctx context.Context) (ToolApprovalResponse, error) {
		if h.turn.conv.approvalFlow == nil {
			return ToolApprovalResponse{}, nil
		}
		approvalFlow := h.turn.conv.approvalFlow
		decision, _, ok := approvalFlow.WaitAndFinalizeApproval(ctx, h.approvalID, WaitApprovalParams[*pendingAIHelperApprovalData]{
			BuildNoDecision: func(reason string, _ *pendingAIHelperApprovalData) *ApprovalDecisionPayload {
				return &ApprovalDecisionPayload{
					ApprovalID: h.approvalID,
					Reason:     reason,
				}
			},
		})
		if !ok {
			return ToolApprovalResponse{Reason: decision.Reason}, nil
		}
		return ToolApprovalResponse{
			Approved: decision.Approved,
			Always:   decision.Always,
			Reason:   decision.Reason,
		}, nil
	})
}

// requestApproval creates a new approval request and returns its handle.
func (t *Turn) requestApproval(req ApprovalRequest) ApprovalHandle {
	t.ensureStarted()
	if t.approvalRequester != nil {
		return t.approvalRequester(t.turnCtx, t, req)
	}
	if t.conv == nil || t.conv.portal == nil || t.conv.approvalFlow == nil {
		return &sdkApprovalHandle{turn: t, toolCallID: req.ToolCallID}
	}
	approvalFlow := t.conv.approvalFlow
	started := approvalFlow.StartApprovalRequest(t.turnCtx, StartApprovalRequestParams[*pendingAIHelperApprovalData]{
		Portal:             t.conv.portal,
		OwnerMXID:          t.conv.login.UserMXID,
		SendPrompt:         true,
		Request:            req,
		NewID:              func() string { return "ai-" + uuid.NewString() },
		DefaultTTL:         DefaultApprovalExpiry,
		DefaultAllowAlways: true,
		PromptContext: ApprovalPromptContext{
			TurnID:            t.turnID,
			ReplyToEventID:    t.InitialEventID(),
			ThreadRootEventID: t.ThreadRoot(),
		},
		EmitRequest: func(ctx context.Context, approvalID, toolCallID string) {
			t.Approvals().EmitRequest(ctx, approvalID, toolCallID)
		},
		Data: &pendingAIHelperApprovalData{
			RoomID:     t.conv.portal.MXID,
			TurnID:     t.turnID,
			ToolCallID: req.ToolCallID,
			ToolName:   req.ToolName,
		},
	})
	return &sdkApprovalHandle{approvalID: started.ApprovalID, toolCallID: req.ToolCallID, turn: t}
}
