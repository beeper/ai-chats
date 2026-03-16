package runtime

import (
	"regexp"
	"strings"
)

var messageIDLineRE = regexp.MustCompile(`(?i)^\s*\[message_id:\s*[^\]]+\]\s*$`)
var messageIDLineExtractRE = regexp.MustCompile(`(?i)^\s*\[message_id:\s*([^\]\r\n]+)\]\s*$`)
var messageIDInlineRE = regexp.MustCompile(`(?i)\[message_id:\s*([^\]\r\n]+)\]`)

func ContainsMessageIDHint(value string) bool {
	return strings.Contains(value, "[message_id:")
}

func NormalizeHintMessageID(value string) string {
	candidate := strings.TrimSpace(strings.Trim(strings.TrimSpace(value), "`\"'<>"))
	if candidate == "" {
		return ""
	}
	// Accept a single token only.
	if strings.ContainsAny(candidate, "[] \t\r\n") {
		return ""
	}
	return candidate
}

func NormalizeMessageID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if match := messageIDLineExtractRE.FindStringSubmatch(trimmed); len(match) > 1 {
		return NormalizeHintMessageID(match[1])
	}
	if match := messageIDInlineRE.FindStringSubmatch(trimmed); len(match) > 1 {
		return NormalizeHintMessageID(match[1])
	}
	return trimmed
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
