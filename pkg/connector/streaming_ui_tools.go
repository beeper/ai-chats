package connector

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
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
	// Emit stream event for real-time UI
	oc.uiEmitter(state).EmitUIToolApprovalRequest(ctx, portal, approvalID, toolCallID, toolName, ttlSeconds)

	// Send timeline message with com.beeper.action_hints buttons
	approvalExpiresAtMs := int64(0)
	if ttlSeconds > 0 {
		approvalExpiresAtMs = time.Now().Add(time.Duration(ttlSeconds) * time.Second).UnixMilli()
	}
	oc.sendActionHintsApprovalEvent(ctx, portal, state, toolCallID, toolName, approvalID, approvalExpiresAtMs)
}

// sendActionHintsApprovalEvent sends a timeline message with com.beeper.action_hints
// containing Allow/Always/Deny buttons for tool approval (MSC1485 pattern).
func (oc *AIClient) sendActionHintsApprovalEvent(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	toolCallID string,
	toolName string,
	approvalID string,
	expiresAtMs int64,
) {
	if portal == nil || portal.MXID == "" {
		return
	}

	var ownerMXID id.UserID
	if oc.UserLogin != nil {
		ownerMXID = oc.UserLogin.UserMXID
	}

	hints := bridgeadapter.BuildApprovalHints(bridgeadapter.ApprovalHintsParams{
		ApprovalID:  approvalID,
		ToolCallID:  toolCallID,
		ToolName:    toolName,
		OwnerMXID:   ownerMXID,
		ExpiresAtMs: expiresAtMs,
	})

	body := fmt.Sprintf("Allow %s tool?", toolName)
	uiMessage := map[string]any{
		"id":   "approval:" + approvalID,
		"role": "assistant",
		"parts": []map[string]any{
			{
				"type":       "action-hints",
				"toolCallId": toolCallID,
				"toolName":   toolName,
			},
		},
	}
	if state != nil && state.turnID != "" {
		uiMessage["metadata"] = map[string]any{"turn_id": state.turnID}
	}

	eventRaw := map[string]any{
		"msgtype":            event.MsgNotice,
		"body":               body,
		BeeperAIKey:          uiMessage,
		BeeperActionHintsKey: hints,
		"m.mentions":         map[string]any{},
	}

	// Track approval event/message IDs for later edits
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:    networkid.PartID("0"),
			Type:  event.EventMessage,
			Extra: eventRaw,
		}},
	}
	if evtID, msgID, err := oc.sendViaPortal(ctx, portal, converted, ""); err == nil && evtID != "" {
		oc.approvals.SetData(approvalID, func(data any) any {
			if d, ok := data.(*pendingToolApprovalData); ok {
				d.ApprovalEventID = evtID
				d.ApprovalNetworkMsgID = msgID
			}
			return data
		})
	}
}
