package stringutil

import (
	"strings"

	"go.mau.fi/util/exslices"
)

// DedupeStrings returns a deduplicated copy of values, preserving order.
// Empty strings and strings that are empty after trimming are skipped.
func DedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	var trimmed []string
	for _, raw := range values {
		if v := strings.TrimSpace(raw); v != "" {
			trimmed = append(trimmed, v)
		}
	}
	return exslices.DeduplicateUnsorted(trimmed)
}
