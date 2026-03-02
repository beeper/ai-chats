package runtime

import "strings"

func SplitTrailingDirective(text string) (string, string) {
	if strings.Contains(text, "[[") {
		openIndex := strings.LastIndex(text, "[[")
		if openIndex >= 0 {
			closeIndex := strings.Index(text[openIndex+2:], "]]")
			if closeIndex < 0 {
				return text[:openIndex], text[openIndex:]
			}
		}
	}
	if body, tail := SplitTrailingMessageIDHint(text); tail != "" {
		return body, tail
	}
	return text, ""
}
