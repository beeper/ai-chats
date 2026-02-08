package connector

import "strings"

func formatSystemAck(text string) string {
	// Keep system notices plain (no emoji prefixes). Trim to avoid awkward leading spaces.
	return strings.TrimSpace(text)
}
