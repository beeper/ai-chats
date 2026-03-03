package bridgeadapter

import (
	"strings"
)

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
