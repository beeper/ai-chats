package runtime

import "strings"

type streamingPendingReplyState struct {
	explicitID string
	sawCurrent bool
	hasTag     bool
}

// StreamingDirectiveAccumulator parses streamed assistant deltas while keeping directive state.
type StreamingDirectiveAccumulator struct {
	pendingTail       string
	pendingReply      streamingPendingReplyState
	pendingWhitespace string
}

func NewStreamingDirectiveAccumulator() *StreamingDirectiveAccumulator {
	return &StreamingDirectiveAccumulator{}
}

func (acc *StreamingDirectiveAccumulator) Consume(raw string, final bool) *StreamingDirectiveResult {
	if acc == nil {
		return nil
	}
	combined := acc.pendingTail + raw
	acc.pendingTail = ""

	if !final {
		body, tail := SplitTrailingDirective(combined)
		combined = body
		acc.pendingTail = tail
	}
	if combined == "" {
		return nil
	}

	parsed := ParseStreamingChunk(combined)
	hasTag := acc.pendingReply.hasTag || parsed.HasReplyTag
	sawCurrent := acc.pendingReply.sawCurrent || parsed.ReplyToCurrent
	explicitID := parsed.ReplyToExplicitID
	if explicitID == "" {
		explicitID = acc.pendingReply.explicitID
	}

	result := &StreamingDirectiveResult{
		Text:              parsed.Text,
		ReplyToExplicitID: explicitID,
		ReplyToCurrent:    sawCurrent,
		HasReplyTag:       hasTag,
		AudioAsVoice:      parsed.AudioAsVoice,
		IsSilent:          parsed.IsSilent,
	}

	if !HasRenderableStreamingContent(result) {
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

func ParseStreamingChunk(raw string) *StreamingDirectiveResult {
	if !strings.Contains(raw, "[[") {
		parsed := &StreamingDirectiveResult{Text: raw}
		if IsSilentReplyText(raw, SilentReplyToken) {
			parsed.IsSilent = true
			parsed.Text = ""
		}
		return parsed
	}

	parsed := ParseInlineDirectives(raw, InlineDirectiveParseOptions{
		StripAudioTag:       true,
		StripReplyTags:      true,
		NormalizeWhitespace: false,
		SilentToken:         SilentReplyToken,
	})
	cleaned := parsed.Text
	if IsSilentReplyText(cleaned, SilentReplyToken) {
		return &StreamingDirectiveResult{
			Text:              "",
			ReplyToExplicitID: parsed.ReplyToExplicitID,
			ReplyToCurrent:    parsed.ReplyToCurrent,
			HasReplyTag:       parsed.HasReplyTag,
			AudioAsVoice:      parsed.AudioAsVoice,
			IsSilent:          true,
		}
	}

	return &StreamingDirectiveResult{
		Text:              cleaned,
		ReplyToExplicitID: parsed.ReplyToExplicitID,
		ReplyToCurrent:    parsed.ReplyToCurrent,
		HasReplyTag:       parsed.HasReplyTag,
		AudioAsVoice:      parsed.AudioAsVoice,
		IsSilent:          parsed.IsSilent,
	}
}

func HasRenderableStreamingContent(result *StreamingDirectiveResult) bool {
	if result == nil {
		return false
	}
	return strings.TrimSpace(result.Text) != ""
}
