package connector

import (
	"context"
	"errors"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

type aiToastType string

const (
	aiToastTypeError aiToastType = "error"
)

func approvalErrorToastText(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrApprovalOnlyOwner):
		return "Only the owner can approve."
	case errors.Is(err, ErrApprovalWrongRoom):
		return "That approval request belongs to a different room."
	case errors.Is(err, ErrApprovalExpired), errors.Is(err, ErrApprovalUnknown):
		return "That approval request is expired or no longer valid."
	case errors.Is(err, ErrApprovalAlreadyHandled):
		return "That approval request was already handled."
	case errors.Is(err, ErrApprovalMissingID):
		return "Missing approval ID."
	default:
		// Keep some context for debugging, but avoid spammy/emoji system notices.
		return strings.TrimSpace(err.Error())
	}
}

// sendApprovalRejectionEvent sends a combined toast + com.beeper.ai snapshot
// marking an approval as output-denied. This is used when resolveToolApproval
// fails (expired/unknown/already-handled) so the desktop can close the modal
// instead of retrying in a loop.
func (oc *AIClient) sendApprovalRejectionEvent(ctx context.Context, portal *bridgev2.Portal, approvalID string, err error) {
	if oc == nil || portal == nil || portal.MXID == "" || approvalID == "" {
		return
	}

	errorText := "Expired"
	switch {
	case errors.Is(err, ErrApprovalAlreadyHandled):
		errorText = "Already handled"
	case errors.Is(err, ErrApprovalOnlyOwner):
		errorText = "Denied"
	case errors.Is(err, ErrApprovalWrongRoom):
		errorText = "Denied"
	}

	toastText := approvalErrorToastText(err)
	raw := map[string]any{
		"msgtype": event.MsgNotice,
		"body":    toastText,
		"com.beeper.ai.toast": map[string]any{
			"text": toastText,
			"type": string(aiToastTypeError),
		},
		BeeperAIKey: map[string]any{
			"id":   "approval:" + approvalID,
			"role": "assistant",
			"parts": []map[string]any{{
				"type":       "dynamic-tool",
				"toolName":   "tool",
				"toolCallId": approvalID,
				"state":      "output-denied",
				"errorText":  errorText,
			}},
		},
		"m.mentions": map[string]any{},
	}
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:    networkid.PartID("0"),
			Type:  event.EventMessage,
			Extra: raw,
		}},
	}
	if _, _, sendErr := oc.sendViaPortal(ctx, portal, converted, ""); sendErr != nil {
		oc.loggerForContext(ctx).Warn().Err(sendErr).Msg("Failed to send approval rejection event")
	}
}
