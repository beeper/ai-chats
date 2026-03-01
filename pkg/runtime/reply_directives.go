package runtime

import "strings"

// ParseReplyDirectives parses reply/silent/audio directives for final assistant text.
func ParseReplyDirectives(raw string, currentMessageID string) ReplyDirectiveResult {
	if strings.TrimSpace(raw) == "" {
		return ReplyDirectiveResult{Text: "", IsSilent: true}
	}
	parsed := ParseInlineDirectives(raw, InlineDirectiveParseOptions{
		CurrentMessageID:    currentMessageID,
		StripAudioTag:       true,
		StripReplyTags:      true,
		NormalizeWhitespace: true,
		SilentToken:         SilentReplyToken,
	})

	result := ReplyDirectiveResult{
		Text:              parsed.Text,
		ReplyToID:         parsed.ReplyToID,
		ReplyToExplicitID: parsed.ReplyToExplicitID,
		ReplyToCurrent:    parsed.ReplyToCurrent,
		HasReplyTag:       parsed.HasReplyTag,
		AudioAsVoice:      parsed.AudioAsVoice,
		IsSilent:          parsed.IsSilent,
	}
	if strings.TrimSpace(result.Text) == "" {
		result.IsSilent = true
	}
	return result
}
