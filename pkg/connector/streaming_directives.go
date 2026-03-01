package connector

import "strings"

type streamingDirectiveAccumulator struct {
	pendingTail       string
	pendingReply      streamingPendingReplyState
	pendingWhitespace string
}

type streamingPendingReplyState struct {
	explicitID string
	sawCurrent bool
	hasTag     bool
}

type streamingDirectiveResult struct {
	Text              string
	ReplyToExplicitID string
	ReplyToCurrent    bool
	HasReplyTag       bool
	IsSilent          bool
}

func newStreamingDirectiveAccumulator() *streamingDirectiveAccumulator {
	return &streamingDirectiveAccumulator{
		pendingReply: streamingPendingReplyState{},
	}
}

func (acc *streamingDirectiveAccumulator) Consume(raw string, final bool) *streamingDirectiveResult {
	if acc == nil {
		return nil
	}
	combined := acc.pendingTail + raw
	acc.pendingTail = ""

	if !final {
		body, tail := splitTrailingDirective(combined)
		combined = body
		acc.pendingTail = tail
	}

	if combined == "" {
		return nil
	}

	parsed := parseStreamingChunk(combined)
	hasTag := acc.pendingReply.hasTag || parsed.HasReplyTag
	sawCurrent := acc.pendingReply.sawCurrent || parsed.ReplyToCurrent
	explicitID := parsed.ReplyToExplicitID
	if explicitID == "" {
		explicitID = acc.pendingReply.explicitID
	}

	result := &streamingDirectiveResult{
		Text:              parsed.Text,
		ReplyToExplicitID: explicitID,
		ReplyToCurrent:    sawCurrent,
		HasReplyTag:       hasTag,
		IsSilent:          parsed.IsSilent,
	}

	if !hasRenderableStreamingContent(result) {
		acc.pendingWhitespace += result.Text
		if hasTag {
			acc.pendingReply = streamingPendingReplyState{
				explicitID: explicitID,
				sawCurrent: sawCurrent,
				hasTag:     hasTag,
			}
		}
		return nil
	}

	if acc.pendingWhitespace != "" {
		result.Text = acc.pendingWhitespace + result.Text
		acc.pendingWhitespace = ""
	}

	acc.pendingReply = streamingPendingReplyState{}
	return result
}

func splitTrailingDirective(text string) (string, string) {
	if !strings.Contains(text, "[") {
		return text, ""
	}
	// Buffer incomplete [[...]] reply directives.
	openIndex := strings.LastIndex(text, "[[")
	if openIndex >= 0 {
		closeIndex := strings.Index(text[openIndex+2:], "]]")
		if closeIndex < 0 {
			return text[:openIndex], text[openIndex:]
		}
	}

	// Buffer incomplete [message_id: ...] or [matrix event id: ...] hints on the
	// trailing line so partial tokens don't leak to the client.
	if body, tail := splitTrailingMessageIDHint(text); tail != "" {
		return body, tail
	}

	return text, ""
}

// splitTrailingMessageIDHint checks whether the last line of text looks like
// the beginning of a [message_id: ...] or [matrix event id: ...] hint that
// hasn't been closed yet. If so it returns (everything-before, trailing-line).
func splitTrailingMessageIDHint(text string) (string, string) {
	// Find the start of the last line.
	idx := strings.LastIndex(text, "\n")
	var prefix, lastLine string
	if idx >= 0 {
		prefix = text[:idx+1]
		lastLine = text[idx+1:]
	} else {
		prefix = ""
		lastLine = text
	}

	trimmed := strings.TrimSpace(lastLine)
	if trimmed == "" {
		return text, ""
	}

	// Fast reject: must start with '['.
	if trimmed[0] != '[' {
		return text, ""
	}

	// If the bracket is already closed, the hint is complete — parseStreamingChunk
	// will strip it, so no need to buffer.
	if strings.Contains(trimmed, "]") {
		return text, ""
	}

	// Check whether the trailing text is a prefix of one of the known hint tags.
	if isMessageIDHintPrefix(strings.ToLower(trimmed)) {
		return prefix, lastLine
	}

	return text, ""
}

// isMessageIDHintPrefix returns true when lower is a case-folded prefix of
// "[message_id:" or "[matrix event id:" (or the target is a prefix of lower,
// meaning lower already contains the full tag opener).
func isMessageIDHintPrefix(lower string) bool {
	for _, target := range []string{"[message_id:", "[matrix event id:"} {
		if strings.HasPrefix(target, lower) || strings.HasPrefix(lower, target) {
			return true
		}
	}
	return false
}

func parseStreamingChunk(raw string) *streamingDirectiveResult {
	// Fast path: skip regex when no directives are possible.
	if !strings.Contains(raw, "[[") && !strings.Contains(raw, "[message_id:") && !strings.Contains(raw, "[matrix event id:") {
		parsed := &streamingDirectiveResult{Text: raw}
		if isSilentReplyText(raw) {
			parsed.IsSilent = true
			parsed.Text = ""
		}
		return parsed
	}

	cleaned := raw
	parsed := &streamingDirectiveResult{}

	cleaned = replyTagRE.ReplaceAllStringFunc(cleaned, func(match string) string {
		parsed.HasReplyTag = true
		submatches := replyTagRE.FindStringSubmatch(match)
		if len(submatches) > 1 && strings.TrimSpace(submatches[1]) != "" {
			parsed.ReplyToExplicitID = strings.TrimSpace(submatches[1])
		} else {
			parsed.ReplyToCurrent = true
		}
		return " "
	})

	// Strip [message_id: ...] hints the model may echo back from the prompt.
	cleaned = stripMessageIDHintLines(cleaned)

	if isSilentReplyText(cleaned) {
		parsed.IsSilent = true
		cleaned = ""
	}

	parsed.Text = cleaned
	return parsed
}

func hasRenderableStreamingContent(result *streamingDirectiveResult) bool {
	if result == nil {
		return false
	}
	return strings.TrimSpace(result.Text) != ""
}
