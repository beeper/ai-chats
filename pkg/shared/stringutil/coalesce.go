package stringutil

import (
	"fmt"
	"strings"
)

// EnvOr returns value (trimmed) if non-empty, otherwise returns existing.
func EnvOr(existing, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return existing
	}
	return value
}

// FirstNonEmpty returns the first non-empty string after trimming.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// StringValue extracts a string from a dynamic value.
// Handles string and fmt.Stringer; returns "" for anything else.
func StringValue(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

// TrimString extracts a string from a dynamic value and trims whitespace.
func TrimString(v any) string {
	return strings.TrimSpace(StringValue(v))
}
