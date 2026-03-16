package runtime

import (
	"strings"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

type streamingPendingReplyState struct {
	explicitID string
	sawCurrent bool
	hasTag     bool
}

// StreamingDirectiveAccumulator parses streamed assistant deltas while keeping directive state.
type StreamingDirectiveAccumulator struct {
	pendingTail  string
	pendingReply streamingPendingReplyState
	activeReply  streamingPendingReplyState
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
	hasTag := acc.activeReply.hasTag || acc.pendingReply.hasTag || parsed.HasReplyTag
	sawCurrent := acc.activeReply.sawCurrent || acc.pendingReply.sawCurrent || parsed.ReplyToCurrent
	explicitID := stringutil.FirstNonEmpty(parsed.ReplyToExplicitID, acc.pendingReply.explicitID, acc.activeReply.explicitID)

	result := &StreamingDirectiveResult{
		Text:              parsed.Text,
		ReplyToExplicitID: explicitID,
		ReplyToCurrent:    sawCurrent,
		HasReplyTag:       hasTag,
		AudioAsVoice:      parsed.AudioAsVoice,
		IsSilent:          parsed.IsSilent,
	}

	if !HasRenderableStreamingContent(result) {
		if hasTag {
			acc.pendingReply = streamingPendingReplyState{
				explicitID: explicitID,
				sawCurrent: sawCurrent,
				hasTag:     hasTag,
			}
		}
		return nil
	}

	acc.activeReply = streamingPendingReplyState{
		explicitID: explicitID,
		sawCurrent: sawCurrent,
		hasTag:     hasTag,
	}
	acc.pendingReply = streamingPendingReplyState{}
	return result
}

// ParseStreamingChunk parses inline directives from a streaming chunk.
func ParseStreamingChunk(raw string) *StreamingDirectiveResult {
	if !strings.Contains(raw, "[[") {
		parsed := &StreamingDirectiveResult{Text: raw}
		if isSilentForStreaming(raw) {
			parsed.IsSilent = true
			parsed.Text = ""
		}
		return parsed
	}

	parsed := ParseInlineDirectives(raw, InlineDirectiveParseOptions{
		StripAudioTag:       false,
		StripReplyTags:      true,
		NormalizeWhitespace: true,
	})
	return parsed.toStreamingResult()
}

// HasRenderableStreamingContent checks whether a streaming result has text or audio to render.
func HasRenderableStreamingContent(result *StreamingDirectiveResult) bool {
	if result == nil {
		return false
	}
	return result.Text != "" || result.AudioAsVoice
}

// SplitTrailingDirective splits text at the last unclosed [[ directive tag,
// returning the body before and the incomplete tag after.
func SplitTrailingDirective(text string) (string, string) {
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
