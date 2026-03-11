package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/bridgeadapter"
)

type aiToastType string

const (
	aiToastTypeError aiToastType = "error"
)

func (oc *AIClient) sendApprovalRequestFallbackEvent(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	approvalID string,
	toolCallID string,
	toolName string,
	replyToEventID id.EventID,
	ttlSeconds int,
) {
	turnID := ""
	if state != nil {
		turnID = state.turnID
	}
	oc.approvalFlow.SendPrompt(ctx, portal, bridgeadapter.SendPromptParams{
		ApprovalPromptMessageParams: bridgeadapter.ApprovalPromptMessageParams{
			ApprovalID:     approvalID,
			ToolCallID:     toolCallID,
			ToolName:       toolName,
			TurnID:         turnID,
			ReplyToEventID: replyToEventID,
			ExpiresAt:      bridgeadapter.ComputeApprovalExpiry(ttlSeconds),
		},
		RoomID:    portal.MXID,
		OwnerMXID: oc.UserLogin.UserMXID,
	})
}

func (oc *AIClient) lookupApprovalSnapshotInfo(approvalID string) (toolCallID, toolName, turnID string) {
	if oc == nil || oc.approvalFlow == nil {
		return "", "", ""
	}
	p := oc.approvalFlow.Get(strings.TrimSpace(approvalID))
	if p == nil || p.Data == nil {
		return "", "", ""
	}
	return strings.TrimSpace(p.Data.ToolCallID), strings.TrimSpace(p.Data.ToolName), strings.TrimSpace(p.Data.TurnID)
}

func buildApprovalSnapshotUIMessage(approvalID, toolCallID, toolName, turnID, state, errorText string) map[string]any {
	approvalID = strings.TrimSpace(approvalID)
	toolCallID = strings.TrimSpace(toolCallID)
	toolName = strings.TrimSpace(toolName)
	turnID = strings.TrimSpace(turnID)
	if toolCallID == "" {
		toolCallID = approvalID
	}
	if toolName == "" {
		toolName = "tool"
	}

	metadata := map[string]any{
		"approvalId": approvalID,
	}
	if turnID != "" {
		metadata["turn_id"] = turnID
	}
	part := map[string]any{
		"type":       "dynamic-tool",
		"toolName":   toolName,
		"toolCallId": toolCallID,
		"state":      state,
	}
	if state == "output-denied" {
		part["approval"] = map[string]any{
			"id":       approvalID,
			"approved": false,
			"reason":   errorText,
		}
		part["errorText"] = errorText
	} else {
		part["approval"] = map[string]any{
			"id": approvalID,
		}
	}
	return map[string]any{
		"id":       approvalID,
		"role":     "assistant",
		"metadata": metadata,
		"parts":    []map[string]any{part},
	}
}

func buildApprovalSnapshotPart(body string, uiMessage map[string]any, toastText string, replyToEventID id.EventID) *bridgev2.ConvertedMessagePart {
	raw := map[string]any{
		"msgtype":    event.MsgNotice,
		"body":       body,
		BeeperAIKey:  uiMessage,
		"m.mentions": map[string]any{},
	}
	if toastText != "" {
		raw["com.beeper.ai.toast"] = map[string]any{
			"text": toastText,
			"type": string(aiToastTypeError),
		}
	}
	if replyToEventID != "" {
		raw["m.relates_to"] = map[string]any{
			"m.in_reply_to": map[string]any{
				"event_id": replyToEventID.String(),
			},
		}
	}
	return &bridgev2.ConvertedMessagePart{
		ID:      networkid.PartID("0"),
		Type:    event.EventMessage,
		Content: &event.MessageEventContent{MsgType: event.MsgNotice, Body: body},
		Extra:   raw,
		DBMetadata: &MessageMetadata{
			BaseMessageMetadata: bridgeadapter.BaseMessageMetadata{
				Role:               "assistant",
				CanonicalSchema:    "ai-sdk-ui-message-v1",
				CanonicalUIMessage: uiMessage,
			},
			ExcludeFromHistory: true,
		},
	}
}
