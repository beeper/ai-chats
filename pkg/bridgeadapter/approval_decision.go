package bridgeadapter

import (
	"strings"
)

type ApprovalDecisionPayload struct {
	ApprovalID string
	Approve    bool
	Always     bool
	Reason     string
}

// ActionDecisionFromString converts an action_id string into structured booleans
// (approve, always, ok).
func ActionDecisionFromString(actionID string) (approve bool, always bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(actionID)) {
	case "allow", "approve", "yes", "y", "true", "1", "once":
		return true, false, true
	case "always", "always-allow", "allow-always":
		return true, true, true
	case "deny", "no", "n", "false", "0", "reject":
		return false, false, true
	default:
		return false, false, false
	}
}

func ParseApprovalDecision(raw map[string]any) (ApprovalDecisionPayload, bool) {
	if raw == nil {
		return ApprovalDecisionPayload{}, false
	}

	approvalID, _ := raw["approval_id"].(string)
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return ApprovalDecisionPayload{}, false
	}

	var approve bool
	var always bool
	var ok bool

	switch value := raw["decision"].(type) {
	case string:
		approve, always, ok = ActionDecisionFromString(value)
	case bool:
		approve = value
		ok = true
	}

	if !ok {
		if value, valueOK := raw["approve"].(bool); valueOK {
			approve = value
			ok = true
		}
	}
	if rawAlways, alwaysOK := raw["always"].(bool); alwaysOK {
		always = rawAlways
	}
	if !ok {
		return ApprovalDecisionPayload{}, false
	}

	reason, _ := raw["reason"].(string)

	return ApprovalDecisionPayload{
		ApprovalID: approvalID,
		Approve:    approve,
		Always:     always,
		Reason:     strings.TrimSpace(reason),
	}, true
}
