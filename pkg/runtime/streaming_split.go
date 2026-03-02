package runtime

import "strings"

func SplitTrailingDirective(text string) (string, string) {
	openIndex := strings.LastIndex(text, "[[")
	if openIndex < 0 {
		return text, ""
	}
	closeIndex := strings.Index(text[openIndex+2:], "]]")
	if closeIndex >= 0 {
		return text, ""
	}
	return text[:openIndex], text[openIndex:]
}
