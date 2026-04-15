package memory

import (
	"context"
	"testing"

	iruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
)

func TestBuildPromptContextTextRespectsInjectContextFlag(t *testing.T) {
	section := "## MEMORY.md\nRemember this"
	deps := PromptContextDeps{
		InjectContext: false,
		ReadSection: func(context.Context, iruntime.Meta, string) string {
			return section
		},
	}
	if got := BuildPromptContextText(context.Background(), nil, nil, deps); got != "" {
		t.Fatalf("expected inject_context=false to suppress memory prompt text, got %q", got)
	}

	deps.InjectContext = true
	if got := BuildPromptContextText(context.Background(), nil, nil, deps); got != section {
		t.Fatalf("expected inject_context=true to include memory prompt text, got %q", got)
	}
}
