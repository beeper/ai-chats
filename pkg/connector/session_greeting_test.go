package connector

import (
	"context"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
)

func TestMaybePrependSessionGreeting(t *testing.T) {
	ctx := context.Background()
	meta := agentModeTestMeta("beeper")
	prompt := []openai.ChatCompletionMessageParamUnion{}

	out := maybePrependSessionGreeting(ctx, nil, meta, prompt, zerolog.Nop())
	if len(out) != 1 {
		t.Fatalf("expected 1 greeting message, got %d", len(out))
	}
	if meta.SessionBootstrapByAgent == nil || meta.SessionBootstrapByAgent["beeper"] == 0 {
		t.Fatal("expected SessionBootstrapByAgent to be set")
	}
	if out[0].OfSystem == nil {
		t.Fatal("expected system message")
	}
	if out[0].OfSystem.Content.OfString.Value != sessionGreetingPrompt {
		t.Fatalf("unexpected greeting content: %q", out[0].OfSystem.Content.OfString.Value)
	}

	out2 := maybePrependSessionGreeting(ctx, nil, meta, []openai.ChatCompletionMessageParamUnion{}, zerolog.Nop())
	if len(out2) != 0 {
		t.Fatalf("expected no additional greeting, got %d", len(out2))
	}
}
