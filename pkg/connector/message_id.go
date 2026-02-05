package connector

import (
	"regexp"
	"strings"

	"maunium.net/go/mautrix/id"
)

var messageIDLineRE = regexp.MustCompile(`(?i)^\s*\[message_id:\s*([^\]]+)\]\s*$`)
var messageIDInlineRE = regexp.MustCompile(`(?i)\[message_id:\s*([^\]]+)\]`)
var matrixEventIDLineRE = regexp.MustCompile(`(?i)^\s*\[matrix event id:\s*([^\]\s]+)(?:\s+room:\s*[^\]]+)?\]\s*$`)

// stripMessageIDHintLines removes full-line [message_id: ...] hints.
// Mirrors OpenClaw's gateway chat sanitization behavior.
func stripMessageIDHintLines(text string) string {
	if !strings.Contains(strings.ToLower(text), "[message_id:") {
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

// normalizeMessageID extracts a raw message id from a hint line or inline tag.
func normalizeMessageID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if match := messageIDLineRE.FindStringSubmatch(trimmed); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	if match := matrixEventIDLineRE.FindStringSubmatch(trimmed); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	if match := messageIDInlineRE.FindStringSubmatch(trimmed); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return trimmed
}

// appendMessageIDHint appends a message_id hint on a new line if one isn't already present.
func appendMessageIDHint(body string, mxid id.EventID) string {
	if mxid == "" || body == "" {
		return body
	}

	body = stripMessageIDHintLines(body)
	trimmed := strings.TrimRight(body, " \t\r\n")
	if trimmed == "" {
		return body
	}
	if strings.Contains(strings.ToLower(body), "[matrix event id:") {
		return body
	}

	lastLine := trimmed
	if idx := strings.LastIndex(trimmed, "\n"); idx >= 0 {
		lastLine = trimmed[idx+1:]
	}
	line := strings.TrimSpace(lastLine)
	if strings.HasPrefix(strings.ToLower(line), "[message_id:") && strings.HasSuffix(line, "]") {
		return body
	}

	sep := "\n"
	if strings.HasSuffix(body, "\n") {
		sep = ""
	}
	return body + sep + "[message_id: " + string(mxid) + "]"
}
