package agents

import (
	"regexp"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

var (
	markdownHeaderRE     = regexp.MustCompile(`^#+(\s|$)`)
	emptyChecklistItemRE = regexp.MustCompile(`^[-*+]\s*(\[[\sXx]?\]\s*)?$`)
)

// DefaultHeartbeatPrompt is the OpenClaw default heartbeat prompt.
const DefaultHeartbeatPrompt = "Read HEARTBEAT.md if it exists (workspace context). Follow it strictly. Do not infer or repeat old tasks from prior chats. If nothing needs attention, reply HEARTBEAT_OK."

// DefaultHeartbeatEvery is the default heartbeat interval (OpenClaw).
const DefaultHeartbeatEvery = "30m"

// IsHeartbeatContentEffectivelyEmpty checks if HEARTBEAT.md has actionable content.
// Returns false when content is empty/missing so the LLM can decide.
func IsHeartbeatContentEffectivelyEmpty(content string) bool {
	if content == "" {
		return true
	}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if markdownHeaderRE.MatchString(trimmed) {
			continue
		}
		if emptyChecklistItemRE.MatchString(trimmed) {
			continue
		}
		return false
	}
	return true
}

// ResolveHeartbeatPrompt returns the configured prompt or the default.
func ResolveHeartbeatPrompt(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return DefaultHeartbeatPrompt
	}
	return trimmed
}

// StripHeartbeatMode controls heartbeat token stripping behavior.
type StripHeartbeatMode string

const (
	StripHeartbeatModeHeartbeat StripHeartbeatMode = "heartbeat"
	StripHeartbeatModeMessage   StripHeartbeatMode = "message"
)

func stripTokenAtEdges(raw string, token string) (string, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", false
	}
	if !strings.Contains(text, token) {
		return text, false
	}
	didStrip := false
	changed := true
	for changed {
		changed = false
		next := strings.TrimSpace(text)
		if strings.HasPrefix(next, token) {
			after := strings.TrimLeft(next[len(token):], " \t\r\n")
			text = after
			didStrip = true
			changed = true
			continue
		}
		if strings.HasSuffix(next, token) {
			before := strings.TrimRight(next[:len(next)-len(token)], " \t\r\n")
			text = before
			didStrip = true
			changed = true
		}
	}
	collapsed := strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	return collapsed, didStrip
}

// StripHeartbeatTokenWithMode strips HEARTBEAT_OK from edges, honoring heartbeat-specific behavior.
// Returns (shouldSkip, strippedText, didStrip).
func StripHeartbeatTokenWithMode(text string, mode StripHeartbeatMode, maxAckChars int) (bool, string, bool) {
	if text == "" {
		return true, "", false
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true, "", false
	}
	if maxAckChars < 0 {
		maxAckChars = 0
	}

	normalized := stringutil.StripMarkup(trimmed)
	hasToken := strings.Contains(trimmed, HeartbeatToken) || strings.Contains(normalized, HeartbeatToken)
	if !hasToken {
		return false, trimmed, false
	}

	origText, origDid := stripTokenAtEdges(trimmed, HeartbeatToken)
	normText, normDid := stripTokenAtEdges(normalized, HeartbeatToken)
	pickedText := ""
	didStrip := false
	if origDid && origText != "" {
		pickedText = origText
		didStrip = true
	} else if normDid {
		pickedText = normText
		didStrip = true
	}

	if !didStrip {
		return false, trimmed, false
	}
	if pickedText == "" {
		return true, "", true
	}
	rest := strings.TrimSpace(pickedText)
	if mode == StripHeartbeatModeHeartbeat && len(rest) <= maxAckChars {
		return true, "", true
	}
	return false, rest, true
}
