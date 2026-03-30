package ai

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
)

func TestSessionGreetingFragment(t *testing.T) {
	ctx := context.Background()
	meta := agentModeTestMeta("beeper")

	greeting := sessionGreetingFragment(ctx, nil, meta, zerolog.Nop())
	if greeting != sessionGreetingPrompt {
		t.Fatalf("expected greeting prompt, got %q", greeting)
	}
	if meta.SessionBootstrapByAgent == nil || meta.SessionBootstrapByAgent["beeper"] == 0 {
		t.Fatal("expected SessionBootstrapByAgent to be set")
	}

	greeting2 := sessionGreetingFragment(ctx, nil, meta, zerolog.Nop())
	if greeting2 != "" {
		t.Fatalf("expected no additional greeting, got %q", greeting2)
	}
}
