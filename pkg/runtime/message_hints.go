package runtime

import (
	"regexp"
	"strings"
)

var messageIDLineRE = regexp.MustCompile(`(?i)^\s*\[message_id:\s*[^\]]+\]\s*$`)

func ContainsMessageIDHint(value string) bool {
	return strings.Contains(value, "[message_id:")
}

func StripMessageIDHintLines(text string) string {
	if !ContainsMessageIDHint(text) {
		return text
	}
	lines := strings.Split(text, "\n")
	changed := false
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if messageIDLineRE.MatchString(line) {
			changed = true
			continue
		}
		filtered = append(filtered, line)
	}
	if !changed {
		return text
	}
	return strings.Join(filtered, "\n")
}
