package stringutil

import "strings"

// NormalizeBaseURL trims whitespace and trailing slashes from a URL.
// If the result is empty, fallback is returned.
func NormalizeBaseURL(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return strings.TrimRight(trimmed, "/")
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
