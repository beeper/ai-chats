package stringutil

import "strings"

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
