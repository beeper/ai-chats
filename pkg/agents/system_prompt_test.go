package agents

import (
	"strings"
	"testing"
)

func TestBuildSystemPrompt_IncludesSoulGuidance(t *testing.T) {
	prompt := BuildSystemPrompt(SystemPromptParams{
		WorkspaceDir: "/",
		PromptMode:   PromptModeFull,
		ContextFiles: []EmbeddedContextFile{{
			Path:    "SOUL.md",
			Content: "Persona",
		}},
	})
	if !strings.Contains(prompt, "If SOUL.md is present, embody its persona and tone") {
		t.Fatalf("expected SOUL guidance in prompt")
	}
	if !strings.Contains(prompt, "## SOUL.md") {
		t.Fatalf("expected SOUL.md section in prompt")
	}
}
