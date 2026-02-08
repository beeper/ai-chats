package connector

import (
	"context"
	"errors"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

type aiToastType string

const (
	aiToastTypeError   aiToastType = "error"
	aiToastTypeNeutral aiToastType = "neutral"
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
	bot := oc.UserLogin.Bridge.Bot
	if bot == nil {
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
	if _, sendErr := bot.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Raw: raw}, nil); sendErr != nil {
		oc.loggerForContext(ctx).Warn().Err(sendErr).Msg("Failed to send approval rejection event")
	}
}

func (oc *AIClient) sendToast(ctx context.Context, portal *bridgev2.Portal, text string, toastType aiToastType) {
	if oc == nil || portal == nil || portal.MXID == "" {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	bot := oc.UserLogin.Bridge.Bot
	if bot == nil {
		return
	}

	raw := map[string]any{
		"msgtype": event.MsgNotice,
		"body":    text,
		"com.beeper.ai.toast": map[string]any{
			"text": text,
			"type": string(toastType),
		},
		"m.mentions": map[string]any{},
	}
	if _, err := bot.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Raw: raw}, nil); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to send toast")
	}
}
