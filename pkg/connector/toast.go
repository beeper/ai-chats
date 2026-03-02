package connector

import (
	"context"
	"errors"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

type aiToastType string

const (
	aiToastTypeError aiToastType = "error"
)

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
	case errors.Is(err, bridgeadapter.ErrApprovalAlreadyHandled):
		errorText = "Already handled"
	case errors.Is(err, bridgeadapter.ErrApprovalOnlyOwner):
		errorText = "Denied"
	case errors.Is(err, bridgeadapter.ErrApprovalWrongRoom):
		errorText = "Denied"
	}

	toastText := bridgeadapter.ApprovalErrorToastText(err)
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
