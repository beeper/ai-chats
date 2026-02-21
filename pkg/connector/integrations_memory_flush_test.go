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

	if meta.MemoryFlushAt != 0 {
		t.Fatalf("expected MemoryFlushAt to remain unset in raw mode, got %d", meta.MemoryFlushAt)
	}
	if meta.MemoryFlushCompactionCount != 0 {
		t.Fatalf("expected MemoryFlushCompactionCount to remain unset in raw mode, got %d", meta.MemoryFlushCompactionCount)
	}
}
