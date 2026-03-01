package connector

import (
	runtimeparse "github.com/beeper/ai-bridge/pkg/runtime"
	"maunium.net/go/mautrix/id"
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

// ParseResponseDirectives extracts directives from LLM response text.
// currentEventID is the triggering message's event ID (used for [[reply_to_current]]).
func ParseResponseDirectives(text string, currentEventID id.EventID) *ResponseDirectives {
	parsed := runtimeparse.ParseReplyDirectives(text, string(currentEventID))
	return &ResponseDirectives{
		Text:           parsed.Text,
		IsSilent:       parsed.IsSilent,
		ReplyToEventID: id.EventID(parsed.ReplyToID),
		ReplyToCurrent: parsed.ReplyToCurrent,
		HasReplyTag:    parsed.HasReplyTag,
	}
}

// isSilentReplyText checks if text starts or ends with the silent token.
// Handles edge cases like markdown/HTML wrapping: **NO_REPLY**, <b>NO_REPLY</b>.
// Matches clawdbot's isSilentReplyText behavior.
func isSilentReplyText(text string) bool {
	return runtimeparse.IsSilentReplyText(text, SilentReplyToken)
}

// normalizeDirectiveWhitespace cleans up whitespace after directive removal.
func normalizeDirectiveWhitespace(text string) string {
	return runtimeparse.NormalizeDirectiveWhitespace(text)
}
