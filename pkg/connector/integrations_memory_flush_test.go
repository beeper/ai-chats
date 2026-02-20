package connector

import (
	"context"
	"testing"

	"github.com/openai/openai-go/v3"
)

func TestMaybeRunMemoryFlush_SkipsInRawMode(t *testing.T) {
	client := &AIClient{}
	meta := &PortalMetadata{IsRawMode: true}

	client.maybeRunMemoryFlush(
		context.Background(),
		nil,
		meta,
		[]openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("latest message"),
		},
	)

	if meta.RecallFlushAt != 0 {
		t.Fatalf("expected RecallFlushAt to remain unset in raw mode, got %d", meta.RecallFlushAt)
	}
	if meta.RecallFlushCompactionCount != 0 {
		t.Fatalf("expected RecallFlushCompactionCount to remain unset in raw mode, got %d", meta.RecallFlushCompactionCount)
	}
}
