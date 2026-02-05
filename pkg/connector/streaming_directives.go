package connector

import "strings"

type streamingDirectiveAccumulator struct {
	pendingTail  string
	pendingReply streamingPendingReplyState
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

func (acc *streamingDirectiveAccumulator) Reset() {
	if acc == nil {
		return
	}
	acc.pendingTail = ""
	acc.pendingReply = streamingPendingReplyState{}
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
		if hasTag {
			acc.pendingReply = streamingPendingReplyState{
				explicitID: explicitID,
				sawCurrent: sawCurrent,
				hasTag:     hasTag,
			}
		}
		return nil
	}

	acc.pendingReply = streamingPendingReplyState{}
	return result
}

func splitTrailingDirective(text string) (string, string) {
	openIndex := strings.LastIndex(text, "[[")
	if openIndex < 0 {
		return text, ""
	}
	closeIndex := strings.Index(text[openIndex+2:], "]]")
	if closeIndex >= 0 {
		return text, ""
	}
	return text[:openIndex], text[openIndex:]
}

func parseStreamingChunk(raw string) *streamingDirectiveResult {
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
