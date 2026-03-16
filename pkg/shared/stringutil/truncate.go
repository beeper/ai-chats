package stringutil

// Truncate returns s unchanged if its length does not exceed maxLen.
// Otherwise it returns the first maxLen bytes followed by "...".
func Truncate(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
