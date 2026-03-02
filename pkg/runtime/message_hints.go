package runtime

import (
	"regexp"
	"strings"
)

var messageIDLineRE = regexp.MustCompile(`(?i)^\s*\[message_id:\s*([^\]\r\n]+)\]\s*$`)
var matrixEventIDLineRE = regexp.MustCompile(`(?i)^\s*\[matrix event id:\s*([^\]\s]+)(?:\s+room:\s*[^\]]+)?\]\s*$`)
var messageIDInlineRE = regexp.MustCompile(`(?i)\[message_id:\s*([^\]\r\n]+)\]`)

func ContainsMessageIDHint(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "[message_id:") || strings.Contains(lower, "[matrix event id:")
}

func NormalizeHintMessageID(value string) string {
	candidate := strings.TrimSpace(value)
	if candidate == "" {
		return ""
	}
	candidate = strings.Trim(candidate, "`\"'<>")
	candidate = strings.TrimSpace(candidate)
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
	if match := messageIDLineRE.FindStringSubmatch(trimmed); len(match) > 1 {
		return NormalizeHintMessageID(match[1])
	}
	if match := matrixEventIDLineRE.FindStringSubmatch(trimmed); len(match) > 1 {
		return NormalizeHintMessageID(match[1])
	}
	if match := messageIDInlineRE.FindStringSubmatch(trimmed); len(match) > 1 {
		return NormalizeHintMessageID(match[1])
	}
	if ContainsMessageIDHint(trimmed) {
		return ""
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
		if messageIDLineRE.MatchString(line) || matrixEventIDLineRE.MatchString(line) {
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

func SplitTrailingMessageIDHint(text string) (string, string) {
	idx := strings.LastIndex(text, "\n")
	var prefix, lastLine string
	if idx >= 0 {
		prefix = text[:idx+1]
		lastLine = text[idx+1:]
	} else {
		lastLine = text
	}
	trimmed := strings.TrimSpace(lastLine)
	if trimmed == "" {
		return text, ""
	}
	if trimmed[0] != '[' {
		return text, ""
	}
	if strings.Contains(trimmed, "]") {
		return text, ""
	}
	if IsMessageIDHintPrefix(strings.ToLower(trimmed)) {
		return prefix, lastLine
	}
	return text, ""
}

func IsMessageIDHintPrefix(lower string) bool {
	for _, target := range []string{"[message_id:", "[matrix event id:"} {
		if strings.HasPrefix(target, lower) || strings.HasPrefix(lower, target) {
			return true
		}
	}
	return false
}
