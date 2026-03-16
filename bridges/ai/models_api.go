package ai

import "strings"

// ResolveAlias is intentionally strict in hard-cut mode: only trim whitespace.
func ResolveAlias(modelID string) string {
	return strings.TrimSpace(modelID)
}
