package codex

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

// pendingToolApprovalDataCodex holds codex-specific metadata stored in
// ApprovalFlow's Pending.Data field.
type pendingToolApprovalDataCodex struct {
	ApprovalID   string
	RoomID       id.RoomID
	ToolCallID   string
	ToolName     string
	Presentation sdk.ApprovalPromptPresentation
}

type codexSDKApprovalHandle struct {
	client     *CodexClient
	turn       *sdk.Turn
	approvalID string
	toolCallID string
}

func (h *codexSDKApprovalHandle) ID() string {
	if h == nil {
		return ""
	}
	return h.approvalID
}

func (h *codexSDKApprovalHandle) ToolCallID() string {
	if h == nil {
		return ""
	}
	return h.toolCallID
}

func (h *codexSDKApprovalHandle) Wait(ctx context.Context) (sdk.ToolApprovalResponse, error) {
	if h == nil || h.client == nil {
		return sdk.ToolApprovalResponse{}, nil
	}
	decision, ok := h.client.waitToolApproval(ctx, h.approvalID)
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = sdk.ApprovalWaitReason(ctx)
	}
	approved := ok && decision.Approved
	if h.turn != nil {
		h.turn.Approvals().Respond(h.turn.Context(), h.approvalID, h.toolCallID, approved, reason)
		if !approved {
			h.turn.Writer().Tools().Denied(h.turn.Context(), h.toolCallID)
		}
	}
	return sdk.ToolApprovalResponse{
		Approved: approved,
		Always:   decision.Always,
		Reason:   reason,
	}, nil
}

func (cc *CodexClient) sendSDKApprovalPrompt(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	turn *sdk.Turn,
	approvalID string,
	ttl time.Duration,
	presentation sdk.ApprovalPromptPresentation,
	toolCallID string,
	toolName string,
) {
	if cc == nil || cc.approvalFlow == nil || cc.UserLogin == nil || portal == nil {
		return
	}
	params := sdk.ApprovalPromptMessageParams{
		ApprovalID:   approvalID,
		ToolCallID:   toolCallID,
		ToolName:     toolName,
		Presentation: presentation,
	}
	if turn != nil {
		params.TurnID = turn.ID()
		params.ReplyToEventID = turn.InitialEventID()
		params.ThreadRootEventID = turn.ThreadRoot()
		params.ExpiresAt = time.Now().Add(ttl)
		cc.approvalFlow.SendPrompt(turn.Context(), portal, sdk.SendPromptParams{
			ApprovalPromptMessageParams: params,
			RoomID:                      portal.MXID,
			OwnerMXID:                   cc.UserLogin.UserMXID,
		})
		return
	}
	if state == nil {
		return
	}
	params.TurnID = state.currentTurnID()
	params.ReplyToEventID = state.currentReplyTargetEventID()
	params.ExpiresAt = sdk.ComputeApprovalExpiry(int(ttl / time.Second))
	cc.approvalFlow.SendPrompt(ctx, portal, sdk.SendPromptParams{
		ApprovalPromptMessageParams: params,
		RoomID:                      portal.MXID,
		OwnerMXID:                   cc.UserLogin.UserMXID,
	})
}

func (cc *CodexClient) requestSDKApproval(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	turn *sdk.Turn,
	req sdk.ApprovalRequest,
) sdk.ApprovalHandle {
	if cc == nil || portal == nil {
		return &codexSDKApprovalHandle{toolCallID: req.ToolCallID}
	}
	approvalID, ttl, presentation := sdk.ResolveApprovalRequest(req, func() string {
		return fmt.Sprintf("codex-%d", time.Now().UnixNano())
	}, sdk.DefaultApprovalExpiry, false)
	cc.setApprovalStateTracking(state, approvalID, req.ToolCallID, req.ToolName)
	cc.registerToolApproval(portal.MXID, approvalID, req.ToolCallID, req.ToolName, presentation, ttl)
	if turn != nil {
		turn.Approvals().EmitRequest(turn.Context(), approvalID, req.ToolCallID)
	} else if state != nil && state.turn != nil {
		state.turn.Approvals().EmitRequest(ctx, approvalID, req.ToolCallID)
	}
	cc.sendSDKApprovalPrompt(ctx, portal, state, turn, approvalID, ttl, presentation, req.ToolCallID, req.ToolName)
	return &codexSDKApprovalHandle{
		client:     cc,
		turn:       turn,
		approvalID: approvalID,
		toolCallID: req.ToolCallID,
	}
}

func (cc *CodexClient) registerToolApproval(
	roomID id.RoomID,
	approvalID, toolCallID, toolName string,
	presentation sdk.ApprovalPromptPresentation,
	ttl time.Duration,
) (*sdk.Pending[*pendingToolApprovalDataCodex], bool) {
	data := &pendingToolApprovalDataCodex{
		ApprovalID:   strings.TrimSpace(approvalID),
		RoomID:       roomID,
		ToolCallID:   strings.TrimSpace(toolCallID),
		ToolName:     strings.TrimSpace(toolName),
		Presentation: presentation,
	}
	return cc.approvalFlow.Register(approvalID, ttl, data)
}

func (cc *CodexClient) waitToolApproval(ctx context.Context, approvalID string) (sdk.ApprovalDecisionPayload, bool) {
	approvalID = strings.TrimSpace(approvalID)
	decision, ok := cc.approvalFlow.Wait(ctx, approvalID)
	if !ok {
		decision = sdk.ApprovalDecisionPayload{
			ApprovalID: approvalID,
			Reason:     sdk.ApprovalWaitReason(ctx),
		}
		cc.approvalFlow.FinishResolved(approvalID, decision)
		return decision, false
	}
	cc.approvalFlow.FinishResolved(approvalID, decision)
	return decision, true
}
