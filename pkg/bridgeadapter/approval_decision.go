package bridgeadapter

import (
	"strings"

	"maunium.net/go/mautrix/event"
)

type ApprovalDecisionPayload struct {
	ApprovalID string
	Approved   bool
	Always     bool
	Reason     string
}

// ParseApprovalDecisionEvent extracts the approval decision payload from a Matrix event's raw content.
func ParseApprovalDecisionEvent(evt *event.Event) (map[string]any, bool) {
	if evt == nil || evt.Content.Raw == nil {
		return nil, false
	}
	raw, ok := evt.Content.Raw["com.beeper.ai.approval_decision"].(map[string]any)
	if !ok {
		return nil, false
	}
	return raw, true
}

func ParseApprovalDecision(raw map[string]any) (ApprovalDecisionPayload, bool) {
	if raw == nil {
		return ApprovalDecisionPayload{}, false
	}

	approvalID, _ := raw["approvalId"].(string)
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return ApprovalDecisionPayload{}, false
	}

	approved, ok := raw["approved"].(bool)
	if !ok {
		return ApprovalDecisionPayload{}, false
	}
	always, _ := raw["always"].(bool)

	reason, _ := raw["reason"].(string)

	return ApprovalDecisionPayload{
		ApprovalID: approvalID,
		Approved:   approved,
		Always:     always,
		Reason:     strings.TrimSpace(reason),
	}, true
}
