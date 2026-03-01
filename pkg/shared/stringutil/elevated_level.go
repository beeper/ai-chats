package stringutil

import "strings"

// NormalizeElevatedLevel normalizes a raw elevated-level string to one of the
// canonical values: "off", "full", "ask", or "on". Returns the normalized
// value and true if recognized, or ("", false) for unrecognized input.
func NormalizeElevatedLevel(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	switch key {
	case "off", "false", "no", "0":
		return "off", true
	case "full", "auto", "auto-approve", "autoapprove":
		return "full", true
	case "ask", "prompt", "approval", "approve":
		return "ask", true
	case "on", "true", "yes", "1":
		return "on", true
	}
	return "", false
}
