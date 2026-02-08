package stringutil

import (
	"regexp"
	"strings"
)

var (
	htmlTagRE          = regexp.MustCompile(`<[^>]*>`)
	mdEmphasisPrefixRE = regexp.MustCompile("^[*`~_]+")
	mdEmphasisSuffixRE = regexp.MustCompile("[*`~_]+$")
)

// StripMarkup removes common HTML/markdown formatting that models might wrap tokens in.
func StripMarkup(text string) string {
	text = htmlTagRE.ReplaceAllString(text, " ")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = mdEmphasisPrefixRE.ReplaceAllString(text, "")
	text = mdEmphasisSuffixRE.ReplaceAllString(text, "")
	return text
}
