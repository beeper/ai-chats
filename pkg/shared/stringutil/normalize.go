package stringutil

import (
	"strings"
)

// NormalizeBaseURL trims whitespace and trailing slashes from a URL.
func NormalizeBaseURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

// NormalizeMimeType lowercases, trims whitespace, and strips parameters from a MIME type.
func NormalizeMimeType(mimeType string) string {
	lower := strings.ToLower(strings.TrimSpace(mimeType))
	if lower == "" {
		return ""
	}
	if semi := strings.IndexByte(lower, ';'); semi >= 0 {
		return strings.TrimSpace(lower[:semi])
	}
	return lower
}

// NormalizeEnum normalizes a raw string to a canonical enum value.
// It lowercases and trims the input, then looks it up in the aliases map.
// Returns the canonical value and true if found, or ("", false) if not.
func NormalizeEnum(raw string, aliases map[string]string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	if val, ok := aliases[key]; ok {
		return val, true
	}
	return "", false
}
