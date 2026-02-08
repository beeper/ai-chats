package stringutil

import "strings"

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
