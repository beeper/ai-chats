package runtime

// ParseReplyDirectives parses reply/silent/audio directives for final assistant text.
func ParseReplyDirectives(raw string, currentMessageID string) ReplyDirectiveResult {
	parsed := ParseInlineDirectives(raw, InlineDirectiveParseOptions{
		CurrentMessageID:    currentMessageID,
		StripAudioTag:       false,
		StripReplyTags:      true,
		NormalizeWhitespace: true,
	})
	return parsed.toReplyResult()
}
