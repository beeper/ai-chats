package codex

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/streamui"
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

type codexApprovalContext struct {
	ctx               context.Context
	turnID            string
	replyToEventID    id.EventID
	threadRootEventID id.EventID
	emitVia           *sdk.Turn
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
	return sdk.WaitToolApprovalHandle(ctx, sdk.WaitToolApprovalHandleParams{
		Turn:             h.turn,
		ApprovalID:       h.approvalID,
		ToolCallID:       h.toolCallID,
		DenyToolOnReject: true,
	}, func(ctx context.Context) (sdk.ToolApprovalResponse, error) {
		decision, ok := h.client.waitToolApproval(ctx, h.approvalID)
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = sdk.ApprovalWaitReason(ctx)
		}
		return sdk.ToolApprovalResponse{
			Approved: ok && decision.Approved,
			Always:   decision.Always,
			Reason:   reason,
		}, nil
	})
}

func resolveCodexApprovalContext(
	ctx context.Context,
	state *streamingState,
	turn *sdk.Turn,
) *codexApprovalContext {
	if turn != nil {
		return &codexApprovalContext{
			ctx:               turn.Context(),
			turnID:            turn.ID(),
			replyToEventID:    turn.InitialEventID(),
			threadRootEventID: turn.ThreadRoot(),
			emitVia:           turn,
		}
	}
	if state == nil || state.turn == nil {
		return nil
	}
	return &codexApprovalContext{
		ctx:            ctx,
		turnID:         state.currentTurnID(),
		replyToEventID: state.currentReplyTargetEventID(),
		emitVia:        state.turn,
	}
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
	approvalCtx := resolveCodexApprovalContext(ctx, state, turn)
	var promptCtx sdk.ApprovalPromptContext
	if approvalCtx != nil {
		promptCtx = sdk.ApprovalPromptContext{
			TurnID:            approvalCtx.turnID,
			ReplyToEventID:    approvalCtx.replyToEventID,
			ThreadRootEventID: approvalCtx.threadRootEventID,
		}
	}
	var emitter sdk.ApprovalRequestEmitter
	if approvalCtx != nil && approvalCtx.emitVia != nil {
		emitter = approvalCtx.emitVia.Approvals()
	}
	started := cc.approvalFlow.StartApprovalRequest(ctx, sdk.StartApprovalRequestParams[*pendingToolApprovalDataCodex]{
		Portal:             portal,
		OwnerMXID:          cc.UserLogin.UserMXID,
		SendPrompt:         true,
		Request:            req,
		NewID:              func() string { return fmt.Sprintf("codex-%d", time.Now().UnixNano()) },
		DefaultTTL:         sdk.DefaultApprovalExpiry,
		DefaultAllowAlways: false,
		PromptContext:      promptCtx,
		Emitter:            emitter,
		Data: &pendingToolApprovalDataCodex{
			ApprovalID:   strings.TrimSpace(req.ApprovalID),
			RoomID:       portal.MXID,
			ToolCallID:   strings.TrimSpace(req.ToolCallID),
			ToolName:     strings.TrimSpace(req.ToolName),
			Presentation: sdk.ApprovalPromptPresentation{},
		},
	})
	approvalID := started.ApprovalID
	presentation := started.Presentation
	if state != nil && state.turn != nil {
		streamui.RecordApprovalRequest(state.turn.UIState(), approvalID, req.ToolCallID, req.ToolName, matrixevents.ToolTypeProvider)
	}
	if started.Pending != nil && started.Pending.Data != nil {
		started.Pending.Data.ApprovalID = approvalID
		started.Pending.Data.RoomID = portal.MXID
		started.Pending.Data.ToolCallID = strings.TrimSpace(req.ToolCallID)
		started.Pending.Data.ToolName = strings.TrimSpace(req.ToolName)
		started.Pending.Data.Presentation = presentation
	}
	return &codexSDKApprovalHandle{
		client:     cc,
		turn:       turn,
		approvalID: approvalID,
		toolCallID: req.ToolCallID,
	}
}

func (cc *CodexClient) waitToolApproval(ctx context.Context, approvalID string) (sdk.ApprovalDecisionPayload, bool) {
	approvalID = strings.TrimSpace(approvalID)
	decision, _, ok := cc.approvalFlow.WaitAndFinalizeApproval(ctx, approvalID, sdk.WaitApprovalParams[*pendingToolApprovalDataCodex]{
		BuildNoDecision: func(reason string, _ *pendingToolApprovalDataCodex) *sdk.ApprovalDecisionPayload {
			return &sdk.ApprovalDecisionPayload{
				ApprovalID: approvalID,
				Reason:     reason,
			}
		},
	})
	return decision, ok
}
