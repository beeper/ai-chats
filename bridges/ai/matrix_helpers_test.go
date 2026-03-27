package ai

import (
	"context"
	"testing"
)

func TestBuildMatrixInboundBodyIncludesSenderMetaForGroup(t *testing.T) {
	client := &AIClient{}
	meta := modelModeTestMeta("openai/gpt-5.2")

	got := client.buildMatrixInboundBody(context.Background(), nil, meta, nil, "  hi  ", "Alice", "Room", true)
	if got != "Alice: hi" {
		t.Fatalf("expected sender-prefixed body, got %q", got)
	}
}
