package connector

import (
	"context"
	"errors"
	"strings"
	"time"

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
	if oc == nil || portal == nil || portal.MXID == "" {
		return
	}
	approvalID = strings.TrimSpace(approvalID)
	toolCallID = strings.TrimSpace(toolCallID)
	toolName = strings.TrimSpace(toolName)
	if approvalID == "" || toolCallID == "" {
		return
	}
	if toolName == "" {
		toolName = "tool"
	}
	turnID := ""
	if state != nil {
		turnID = state.turnID
	}
	expiresAt := time.Now().Add(10 * time.Minute)
	if ttlSeconds > 0 {
		expiresAt = time.Now().Add(time.Duration(ttlSeconds) * time.Second)
	}
	prompt := bridgeadapter.BuildApprovalPromptMessage(bridgeadapter.ApprovalPromptMessageParams{
		ApprovalID:     approvalID,
		ToolCallID:     toolCallID,
		ToolName:       toolName,
		TurnID:         turnID,
		ReplyToEventID: replyToEventID,
		ExpiresAt:      expiresAt,
	})
	uiMessage := prompt.UIMessage
	if oc.approvalPrompts != nil {
		oc.approvalPrompts.Register(bridgeadapter.ApprovalPromptRegistration{
			ApprovalID: approvalID,
			RoomID:     portal.MXID,
			OwnerMXID:  oc.UserLogin.UserMXID,
			ToolCallID: toolCallID,
			ToolName:   toolName,
			TurnID:     turnID,
			ExpiresAt:  expiresAt,
			Options:    prompt.Options,
		})
	}
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{
			{
				ID:      networkid.PartID("0"),
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgNotice, Body: prompt.Body},
				Extra:   prompt.Raw,
				DBMetadata: &MessageMetadata{
					BaseMessageMetadata: bridgeadapter.BaseMessageMetadata{
						Role:               "assistant",
						CanonicalSchema:    "ai-sdk-ui-message-v1",
						CanonicalUIMessage: uiMessage,
					},
					ExcludeFromHistory: true,
				},
			},
		},
	}
	eventID, _, err := oc.sendViaPortal(ctx, portal, converted, "")
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("approval_id", approvalID).Msg("Failed to send approval request fallback event")
		return
	}
	if oc.approvalPrompts != nil {
		oc.approvalPrompts.BindPromptEvent(approvalID, eventID)
	}
	seenKeys := map[string]struct{}{}
	for _, option := range prompt.Options {
		for _, key := range bridgeadapter.ApprovalOptionPrefillKeys(option) {
			if key == "" {
				continue
			}
			if _, exists := seenKeys[key]; exists {
				continue
			}
			seenKeys[key] = struct{}{}
			oc.sendReaction(ctx, portal, eventID, key)
		}
	}
}

// sendApprovalRejectionEvent sends a combined toast + com.beeper.ai snapshot
// marking an approval as output-denied. This is used when resolveToolApproval
// fails (expired/unknown/already-handled) so the desktop can close the modal
// instead of retrying in a loop.
func (oc *AIClient) sendApprovalRejectionEvent(ctx context.Context, portal *bridgev2.Portal, approvalID string, err error, replyToEventID id.EventID) {
	if oc == nil || portal == nil || portal.MXID == "" {
		return
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}

	errorText := "Expired"
	switch {
	case errors.Is(err, bridgeadapter.ErrApprovalAlreadyHandled):
		errorText = "Already handled"
	case errors.Is(err, bridgeadapter.ErrApprovalOnlyOwner):
		errorText = "Denied"
	case errors.Is(err, bridgeadapter.ErrApprovalWrongRoom):
		errorText = "Denied"
	}

	toastText := bridgeadapter.ApprovalErrorToastText(err)
	toolCallID, toolName, turnID := oc.lookupApprovalSnapshotInfo(approvalID)
	uiMessage := buildApprovalSnapshotUIMessage(approvalID, toolCallID, toolName, turnID, "output-denied", errorText)
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{
			buildApprovalSnapshotPart(toastText, uiMessage, toastText, replyToEventID),
		},
	}
	if _, _, sendErr := oc.sendViaPortal(ctx, portal, converted, ""); sendErr != nil {
		oc.loggerForContext(ctx).Warn().Err(sendErr).Msg("Failed to send approval rejection event")
	}
}

func (oc *AIClient) lookupApprovalSnapshotInfo(approvalID string) (toolCallID, toolName, turnID string) {
	if oc == nil || oc.approvals == nil {
		return "", "", ""
	}
	p := oc.approvals.Get(strings.TrimSpace(approvalID))
	if p == nil {
		return "", "", ""
	}
	data := approvalData(p)
	return strings.TrimSpace(data.ToolCallID), strings.TrimSpace(data.ToolName), strings.TrimSpace(data.TurnID)
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
