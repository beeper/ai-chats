package streamtransport

import (
	"strings"
	"unicode/utf8"
)

const MaxMatrixEventBodyBytes = 60000

// SplitAtMarkdownBoundary splits text at a paragraph/line boundary near maxBytes.
// Returns (first, rest). If text fits, rest is empty.
func SplitAtMarkdownBoundary(text string, maxBytes int) (string, string) {
	if len(text) <= maxBytes {
		return text, ""
	}
	cutoff := text[:maxBytes]
	for len(cutoff) > 0 && !utf8.ValidString(cutoff) {
		cutoff = cutoff[:len(cutoff)-1]
	}
	cutLen := len(cutoff)
	if idx := strings.LastIndex(cutoff, "\n\n"); idx > cutLen/2 {
		return text[:idx], text[idx:]
	}
	if idx := strings.LastIndex(cutoff, "\n"); idx > cutLen/2 {
		return text[:idx], text[idx:]
	}
	return cutoff, text[cutLen:]
}
