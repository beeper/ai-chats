package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/bridges/codex/codexrpc"
	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"github.com/beeper/ai-bridge/pkg/matrixevents"
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

// ToolApprovalDecisionCodex represents a user's decision on a tool approval request.
type ToolApprovalDecisionCodex struct {
	Approve   bool
	Reason    string
	DecidedAt time.Time
	DecidedBy id.UserID
}

// pendingToolApprovalDataCodex holds codex-specific metadata stored in
// ApprovalManager's PendingApproval.Data field.
type pendingToolApprovalDataCodex struct {
	ApprovalID           string
	ToolCallID           string
	ToolName             string
	ApprovalEventID      id.EventID
	ApprovalNetworkMsgID networkid.MessageID
}

func (cc *CodexClient) registerToolApproval(approvalID, toolCallID, toolName string, ttl time.Duration) (*bridgeadapter.PendingApproval[ToolApprovalDecisionCodex], bool) {
	data := &pendingToolApprovalDataCodex{
		ApprovalID: strings.TrimSpace(approvalID),
		ToolCallID: strings.TrimSpace(toolCallID),
		ToolName:   strings.TrimSpace(toolName),
	}
	return cc.approvals.Register(approvalID, ttl, data)
}

func (cc *CodexClient) resolveToolApproval(approvalID string, decision ToolApprovalDecisionCodex) error {
	return cc.approvals.Resolve(approvalID, decision)
}

func (cc *CodexClient) waitToolApproval(ctx context.Context, approvalID string) (ToolApprovalDecisionCodex, bool) {
	return cc.approvals.Wait(ctx, approvalID)
}

// handleApprovalRequest is the shared handler for command and file-change approval RPC requests.
func (cc *CodexClient) handleApprovalRequest(
	ctx context.Context, req codexrpc.Request,
	defaultToolName string, extractInput func(json.RawMessage) map[string]any,
) (any, *codexrpc.RPCError) {
	approvalID := strings.Trim(string(req.ID), "\"")
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
	}
	_ = json.Unmarshal(req.Params, &params)

	cc.activeMu.Lock()
	active := cc.activeTurns[codexTurnKey(params.ThreadID, params.TurnID)]
	cc.activeMu.Unlock()
	if active == nil || params.ThreadID != active.threadID || params.TurnID != active.turnID {
		return map[string]any{"decision": "decline"}, nil
	}

	toolCallID := strings.TrimSpace(params.ItemID)
	if toolCallID == "" {
		toolCallID = defaultToolName
	}
	toolName := defaultToolName
	ttlSeconds := 600

	cc.setApprovalStateTracking(active.state, approvalID, toolCallID, toolName)

	inputMap := extractInput(req.Params)
	cc.ensureUIToolInputStart(ctx, active.portal, active.state, toolCallID, toolName, true, inputMap)
	approvalTTL := time.Duration(ttlSeconds) * time.Second
	cc.registerToolApproval(approvalID, toolCallID, toolName, approvalTTL)

	cc.emitUIToolApprovalRequest(ctx, active.portal, active.state, approvalID, toolCallID, toolName, ttlSeconds)
	approvalExpiresAtMs := time.Now().Add(approvalTTL).UnixMilli()
	cc.sendToolCallApprovalEvent(ctx, active.portal, active.state, toolCallID, toolName, approvalID, approvalExpiresAtMs)
	cc.sendActionHintsApprovalEvent(ctx, active.portal, active.state, toolCallID, toolName, approvalID, approvalExpiresAtMs)
	cc.sendSystemNoticeOnce(ctx, active.portal, active.state, "codex-approval:"+approvalID, fmt.Sprintf("Approval required (%s): !ai approve %s <allow|deny> [reason]", toolName, approvalID))

	if active.meta != nil {
		if lvl, _ := stringutil.NormalizeElevatedLevel(active.meta.ElevatedLevel); lvl == "full" {
			return map[string]any{"decision": "accept"}, nil
		}
	}

	decision, ok := cc.waitToolApproval(ctx, approvalID)
	if !ok {
		return map[string]any{"decision": "decline"}, nil
	}
	if decision.Approve {
		return map[string]any{"decision": "accept"}, nil
	}
	return map[string]any{"decision": "decline"}, nil
}

func (cc *CodexClient) handleCommandApprovalRequest(ctx context.Context, req codexrpc.Request) (any, *codexrpc.RPCError) {
	return cc.handleApprovalRequest(ctx, req, "commandExecution", func(raw json.RawMessage) map[string]any {
		var p struct {
			Command *string `json:"command"`
			Cwd     *string `json:"cwd"`
			Reason  *string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &p)
		return map[string]any{"command": p.Command, "cwd": p.Cwd, "reason": p.Reason}
	})
}

func (cc *CodexClient) handleFileChangeApprovalRequest(ctx context.Context, req codexrpc.Request) (any, *codexrpc.RPCError) {
	return cc.handleApprovalRequest(ctx, req, "fileChange", func(raw json.RawMessage) map[string]any {
		var p struct {
			Reason    *string `json:"reason"`
			GrantRoot *string `json:"grantRoot"`
		}
		_ = json.Unmarshal(raw, &p)
		return map[string]any{"reason": p.Reason, "grantRoot": p.GrantRoot}
	})
}

// setApprovalStateTracking populates the streaming state maps used for approval correlation.
func (cc *CodexClient) setApprovalStateTracking(state *streamingState, approvalID, toolCallID, toolName string) {
	if state == nil {
		return
	}
	state.ui.InitMaps()
	state.ui.UIToolCallIDByApproval[approvalID] = toolCallID
	state.ui.UIToolApprovalRequested[approvalID] = true
	state.ui.UIToolNameByToolCallID[toolCallID] = toolName
	state.ui.UIToolTypeByToolCallID[toolCallID] = matrixevents.ToolTypeProvider
}
