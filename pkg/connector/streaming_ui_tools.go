package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
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

	ownerMXID := ""
	if oc.UserLogin != nil {
		ownerMXID = oc.UserLogin.UserMXID.String()
	}

	contextData, _ := json.Marshal(map[string]any{
		"approval_id":  approvalID,
		"tool_name":    toolName,
		"tool_call_id": toolCallID,
	})

	hints := &event.BeeperActionHints{
		Hints: []event.BeeperActionHint{
			{
				Body:      "Allow",
				EventType: "com.beeper.action_response",
				Event:     json.RawMessage(`{"action_id":"allow"}`),
			},
			{
				Body:      "Always Allow",
				EventType: "com.beeper.action_response",
				Event:     json.RawMessage(`{"action_id":"always"}`),
			},
			{
				Body:      "Deny",
				EventType: "com.beeper.action_response",
				Event:     json.RawMessage(`{"action_id":"deny"}`),
			},
		},
		Exclusive: true,
		Context:   contextData,
	}
	if ownerMXID != "" {
		hints.AllowedSenders = []id.UserID{id.UserID(ownerMXID)}
	}
	if expiresAtMs > 0 {
		hints.ExpiresAt = expiresAtMs
	}

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
			ID:    "0",
			Type:  event.EventMessage,
			Extra: eventRaw,
		}},
	}
	if evtID, msgID, err := oc.sendViaPortal(ctx, portal, converted, ""); err == nil && evtID != "" {
		oc.toolApprovalsMu.Lock()
		if p := oc.toolApprovals[approvalID]; p != nil {
			p.ApprovalEventID = evtID
			p.ApprovalNetworkMsgID = msgID
		}
		oc.toolApprovalsMu.Unlock()
	}
}
