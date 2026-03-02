package runtime

import "strings"

func SplitTrailingDirective(text string) (string, string) {
	if !strings.Contains(text, "[[") {
		return text, ""
	}
	openIndex := strings.LastIndex(text, "[[")
	if openIndex < 0 {
		return text, ""
	}
	closeIndex := strings.Index(text[openIndex+2:], "]]")
	if closeIndex < 0 {
		return text[:openIndex], text[openIndex:]
	}
	return text, ""
}
