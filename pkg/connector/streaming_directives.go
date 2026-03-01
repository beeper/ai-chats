package connector

import (
	runtimeparse "github.com/beeper/ai-bridge/pkg/runtime"
)

type streamingDirectiveAccumulator struct {
	inner *runtimeparse.StreamingDirectiveAccumulator
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
		inner: runtimeparse.NewStreamingDirectiveAccumulator(),
	}
}

func (acc *streamingDirectiveAccumulator) Consume(raw string, final bool) *streamingDirectiveResult {
	if acc == nil {
		return nil
	}
	if acc.inner == nil {
		acc.inner = runtimeparse.NewStreamingDirectiveAccumulator()
	}
	parsed := acc.inner.Consume(raw, final)
	if parsed == nil {
		return nil
	}
	result := &streamingDirectiveResult{
		Text:              parsed.Text,
		ReplyToExplicitID: parsed.ReplyToExplicitID,
		ReplyToCurrent:    parsed.ReplyToCurrent,
		HasReplyTag:       parsed.HasReplyTag,
		IsSilent:          parsed.IsSilent,
	}
	return result
}

func splitTrailingDirective(text string) (string, string) {
	return runtimeparse.SplitTrailingDirective(text)
}

// splitTrailingMessageIDHint checks whether the last line of text looks like
// the beginning of a [message_id: ...] or [matrix event id: ...] hint that
// hasn't been closed yet. If so it returns (everything-before, trailing-line).
func splitTrailingMessageIDHint(text string) (string, string) {
	return runtimeparse.SplitTrailingMessageIDHint(text)
}

// isMessageIDHintPrefix returns true when lower is a case-folded prefix of
// "[message_id:" or "[matrix event id:" (or the target is a prefix of lower,
// meaning lower already contains the full tag opener).
func isMessageIDHintPrefix(lower string) bool {
	return runtimeparse.IsMessageIDHintPrefix(lower)
}

func parseStreamingChunk(raw string) *streamingDirectiveResult {
	parsed := runtimeparse.ParseStreamingChunk(raw)
	if parsed == nil {
		return nil
	}
	return &streamingDirectiveResult{
		Text:              parsed.Text,
		ReplyToExplicitID: parsed.ReplyToExplicitID,
		ReplyToCurrent:    parsed.ReplyToCurrent,
		HasReplyTag:       parsed.HasReplyTag,
		IsSilent:          parsed.IsSilent,
	}
}

func hasRenderableStreamingContent(result *streamingDirectiveResult) bool {
	if result == nil {
		return false
	}
	return runtimeparse.HasRenderableStreamingContent(&runtimeparse.StreamingDirectiveResult{Text: result.Text})
}
