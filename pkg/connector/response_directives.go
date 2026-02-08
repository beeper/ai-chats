package connector

import (
	"regexp"
	"strings"

	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

// SilentReplyToken is the token the agent uses to indicate no response is needed.
// Matches clawdbot/OpenClaw's SILENT_REPLY_TOKEN.
const SilentReplyToken = "NO_REPLY"

// ResponseDirectives contains parsed directives from an LLM response.
// Matches OpenClaw's directive parsing behavior.
type ResponseDirectives struct {
	// Text is the cleaned response text with directives stripped.
	Text string

	// IsSilent indicates the response should not be sent (NO_REPLY token present).
	IsSilent bool

	// ReplyToEventID is the Matrix event ID to reply to (from [[reply_to:<id>]] or [[reply_to_current]]).
	ReplyToEventID id.EventID

	// ReplyToCurrent indicates [[reply_to_current]] was used (reply to triggering message).
	ReplyToCurrent bool

	// HasReplyTag indicates a reply tag was present in the original text.
	HasReplyTag bool
}

var (
	// replyTagRE matches [[reply_to_current]] or [[reply_to:<id>]]
	// Allows whitespace inside brackets: [[ reply_to_current ]] or [[ reply_to: abc123 ]]
	// Matches OpenClaw's REPLY_TAG_RE pattern.
	replyTagRE = regexp.MustCompile(`\[\[\s*(?:reply_to_current|reply_to\s*:\s*([^\]\n]+))\s*\]\]`)

	// silentPrefixRE matches NO_REPLY at the start (with optional whitespace)
	// Matches OpenClaw's isSilentReplyText prefix check.
	silentPrefixRE = regexp.MustCompile(`^\s*` + regexp.QuoteMeta(SilentReplyToken) + `(?:$|\W)`)

	// silentSuffixRE matches NO_REPLY at the end (word boundary)
	// Matches OpenClaw's isSilentReplyText suffix check.
	silentSuffixRE = regexp.MustCompile(`\b` + regexp.QuoteMeta(SilentReplyToken) + `\b\W*$`)

	collapseSpacesRE    = regexp.MustCompile(`[ \t]+`)
	normalizeNewlinesRE = regexp.MustCompile(`[ \t]*\n[ \t]*`)
	collapseNewlinesRE  = regexp.MustCompile(`\n{3,}`)
)

// ParseResponseDirectives extracts directives from LLM response text.
// currentEventID is the triggering message's event ID (used for [[reply_to_current]]).
func ParseResponseDirectives(text string, currentEventID id.EventID) *ResponseDirectives {
	if text == "" {
		return &ResponseDirectives{IsSilent: true}
	}

	result := &ResponseDirectives{}
	cleaned := text

	// Check for silent reply token (at start or end)
	if isSilentReplyText(text) {
		result.IsSilent = true
		// Still parse other directives but mark as silent
	}

	// Parse reply tags
	var sawCurrent bool
	var lastExplicitID string

	cleaned = replyTagRE.ReplaceAllStringFunc(cleaned, func(match string) string {
		result.HasReplyTag = true
		submatches := replyTagRE.FindStringSubmatch(match)
		if len(submatches) > 1 && submatches[1] != "" {
			// Explicit ID: [[reply_to:<id>]]
			lastExplicitID = strings.TrimSpace(submatches[1])
		} else {
			// [[reply_to_current]]
			sawCurrent = true
		}
		return " " // Replace with space to maintain word boundaries
	})

	// Resolve reply target (explicit ID takes precedence)
	// Matches OpenClaw's logic where explicit reply_to:<id> overrides reply_to_current.
	if lastExplicitID != "" {
		result.ReplyToEventID = id.EventID(lastExplicitID)
	} else if sawCurrent && currentEventID != "" {
		result.ReplyToEventID = currentEventID
		result.ReplyToCurrent = true
	}

	// Note: Reactions are handled via the message tool (action=react), not inline tags.
	// This matches OpenClaw's approach.

	// Normalize whitespace
	cleaned = normalizeDirectiveWhitespace(cleaned)
	result.Text = cleaned

	// If only whitespace remains after stripping, treat as silent
	if strings.TrimSpace(cleaned) == "" {
		result.IsSilent = true
	}

	return result
}

// isSilentReplyText checks if text starts or ends with the silent token.
// Handles edge cases like markdown/HTML wrapping: **NO_REPLY**, <b>NO_REPLY</b>.
// Matches clawdbot's isSilentReplyText behavior.
func isSilentReplyText(text string) bool {
	if text == "" {
		return false
	}

	// Strip markup first (handles **NO_REPLY**, <b>NO_REPLY</b>, etc.)
	stripped := stringutil.StripMarkup(text)

	// Exact match after stripping
	if strings.TrimSpace(stripped) == SilentReplyToken {
		return true
	}

	// Check prefix
	if silentPrefixRE.MatchString(stripped) {
		return true
	}
	// Check suffix
	return silentSuffixRE.MatchString(stripped)
}

// normalizeDirectiveWhitespace cleans up whitespace after directive removal.
func normalizeDirectiveWhitespace(text string) string {
	text = collapseSpacesRE.ReplaceAllString(text, " ")
	text = normalizeNewlinesRE.ReplaceAllString(text, "\n")
	text = collapseNewlinesRE.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// StripSilentToken removes the silent token from text if present.
// Returns the cleaned text.
func StripSilentToken(text string) string {
	// Strip markup first to handle wrapped tokens
	stripped := stringutil.StripMarkup(text)
	// Remove from start
	stripped = silentPrefixRE.ReplaceAllString(stripped, "")
	// Remove from end
	stripped = silentSuffixRE.ReplaceAllString(stripped, "")
	return strings.TrimSpace(stripped)
}
