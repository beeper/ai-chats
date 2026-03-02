package runtime

// ParseReplyDirectives parses reply/silent/audio directives for final assistant text.
func ParseReplyDirectives(raw string, currentMessageID string) ReplyDirectiveResult {
	parsed := ParseInlineDirectives(raw, InlineDirectiveParseOptions{
		CurrentMessageID:    currentMessageID,
		StripAudioTag:       false,
		StripReplyTags:      true,
		NormalizeWhitespace: true,
	})
	text := parsed.Text
	if parsed.HasReplyTag {
		text = parsed.Text
	}
	isSilent := IsSilentReplyText(text, SilentReplyToken)
	if isSilent {
		text = ""
	}

	result := ReplyDirectiveResult{
		Text:              text,
		ReplyToID:         parsed.ReplyToID,
		ReplyToExplicitID: parsed.ReplyToExplicitID,
		ReplyToCurrent:    parsed.ReplyToCurrent,
		HasReplyTag:       parsed.HasReplyTag,
		AudioAsVoice:      parsed.AudioAsVoice,
		IsSilent:          isSilent,
	}
	return result
}
