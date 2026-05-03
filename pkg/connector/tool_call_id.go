package connector

import (
	"regexp"
	"strings"
)

var nonAlphanumericRE = regexp.MustCompile(`[^a-zA-Z0-9]`)

// SanitizeToolCallID cleans a tool call ID for provider compatibility.
//
// Modes:
//   - "strict": strips all non-alphanumeric characters, preserves "call_" prefix
//   - "strict9": strips non-alphanumeric, truncates to 9 chars (some providers require short IDs)
//
// If the ID is empty after sanitization, a new random call ID is generated.
func SanitizeToolCallID(id string, mode string) string {
	if strings.TrimSpace(id) == "" {
		return NewCallID()
	}

	// Preserve the "call_" prefix convention if present
	prefix := ""
	body := id
	if strings.HasPrefix(id, "call_") {
		prefix = "call_"
		body = id[5:]
	}

	sanitized := nonAlphanumericRE.ReplaceAllString(body, "")

	switch mode {
	case "strict9":
		if len(sanitized) > 9 {
			sanitized = sanitized[:9]
		}
	}

	if sanitized == "" {
		return NewCallID()
	}

	return prefix + sanitized
}
