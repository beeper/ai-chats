package runtime

import "strings"

// ParseReplyDirectives parses reply/silent/audio directives for final assistant text.
func ParseReplyDirectives(raw string, currentMessageID string) ReplyDirectiveResult {
	parsed := ParseInlineDirectives(raw, InlineDirectiveParseOptions{
		CurrentMessageID:    currentMessageID,
		StripAudioTag:       false,
		StripReplyTags:      true,
		NormalizeWhitespace: true,
	})
	text := parsed.Text
	isSilent := IsSilentReplyText(text, SilentReplyToken)
	if isSilent {
		text = ""
	}
	return ReplyDirectiveResult{
		Text:              text,
		ReplyToID:         parsed.ReplyToID,
		ReplyToExplicitID: parsed.ReplyToExplicitID,
		ReplyToCurrent:    parsed.ReplyToCurrent,
		HasReplyTag:       parsed.HasReplyTag,
		AudioAsVoice:      parsed.AudioAsVoice,
		IsSilent:          isSilent,
	}
}

// IsRenderablePayload checks whether a reply payload has any content to send.
func IsRenderablePayload(payload ReplyPayload) bool {
	return strings.TrimSpace(payload.Text) != "" ||
		strings.TrimSpace(payload.MediaURL) != "" ||
		len(payload.MediaURLs) > 0
}

// NormalizeReplyPayloadDirectives applies directive parsing to a payload and returns
// the updated payload along with whether it is silent.
func NormalizeReplyPayloadDirectives(payload ReplyPayload, currentMessageID string) (ReplyPayload, bool) {
	parsed := ParseReplyDirectives(payload.Text, currentMessageID)
	payload.Text = parsed.Text
	if payload.ReplyToID == "" {
		payload.ReplyToID = parsed.ReplyToID
	}
	payload.ReplyToCurrent = payload.ReplyToCurrent || parsed.ReplyToCurrent
	payload.ReplyToTag = payload.ReplyToTag || parsed.HasReplyTag
	payload.AudioAsVoice = payload.AudioAsVoice || parsed.AudioAsVoice
	return payload, parsed.IsSilent
}
