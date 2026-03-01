package bridgeadapter

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/maputil"
)

// ApprovalDecisionPayload holds the parsed content of a
// "com.beeper.ai.approval_decision" event payload.
type ApprovalDecisionPayload struct {
	ApprovalID string
	Decision   string
	Reason     string
}

// ParseApprovalDecision extracts an ApprovalDecisionPayload from the raw
// event content map, returning nil when the payload is absent or incomplete.
func ParseApprovalDecision(raw map[string]any) *ApprovalDecisionPayload {
	if raw == nil {
		return nil
	}
	payloadRaw, ok := raw["com.beeper.ai.approval_decision"]
	if !ok || payloadRaw == nil {
		return nil
	}
	payloadMap, ok := payloadRaw.(map[string]any)
	if !ok {
		return nil
	}
	approvalID := strings.TrimSpace(maputil.StringArg(payloadMap, "approvalId"))
	decision := strings.TrimSpace(maputil.StringArg(payloadMap, "decision"))
	reason := strings.TrimSpace(maputil.StringArg(payloadMap, "reason"))
	if approvalID == "" || decision == "" {
		return nil
	}
	return &ApprovalDecisionPayload{
		ApprovalID: approvalID,
		Decision:   decision,
		Reason:     reason,
	}
}

// ApprovalDecisionFromString converts a free-text decision string into
// structured booleans (approve, always, ok).
func ApprovalDecisionFromString(decision string) (approve bool, always bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(decision)) {
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
