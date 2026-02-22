package connector

import (
	"context"
	"testing"

	"github.com/openai/openai-go/v3"

	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
)

func TestMaybeRunMemoryFlush_SkipsInRawMode(t *testing.T) {
	client := &AIClient{}
	host := &runtimeIntegrationHost{client: client}
	meta := &PortalMetadata{IsRawMode: true}

	_, _, _ = host.OnContextOverflow(
		context.Background(),
		integrationruntime.ContextOverflowCall{
			Meta: meta,
			Prompt: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("latest message"),
			},
		},
	)

	if meta.MemoryFlushAt != 0 {
		t.Fatalf("expected MemoryFlushAt to remain unset in raw mode, got %d", meta.MemoryFlushAt)
	}
	if meta.MemoryFlushCompactionCount != 0 {
		t.Fatalf("expected MemoryFlushCompactionCount to remain unset in raw mode, got %d", meta.MemoryFlushCompactionCount)
	}
}
