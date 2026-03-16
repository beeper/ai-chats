package ai

import (
	"context"
	"testing"
)

func TestBuildMatrixInboundBody_SimpleModeBypassesEnvelopeAndSenderMeta(t *testing.T) {
	client := &AIClient{}
	meta := &PortalMetadata{ResolvedTarget: &ResolvedTarget{Kind: ResolvedTargetModel, GhostID: modelUserID("openai/gpt-5.2"), ModelID: "openai/gpt-5.2"}}

	got := client.buildMatrixInboundBody(context.Background(), nil, meta, nil, "  hi  ", "Alice", "Room", true)
	if got != "hi" {
		t.Fatalf("expected raw body only, got %q", got)
	}
}
