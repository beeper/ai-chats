package connector

import (
	"testing"

	"github.com/openai/openai-go/v3"
)

func TestBuildCompactionSummarizationInstructions(t *testing.T) {
	got := buildCompactionSummarizationInstructions("", "strict", "")
	if got == "" {
		t.Fatal("expected strict identifier instructions by default")
	}
	got = buildCompactionSummarizationInstructions("Keep code snippets.", "off", "")
	if got != "Additional focus:\nKeep code snippets." {
		t.Fatalf("unexpected custom-only instructions: %q", got)
	}
	got = buildCompactionSummarizationInstructions("", "custom", "Keep ticket IDs exactly.")
	if got != "Keep ticket IDs exactly." {
		t.Fatalf("unexpected custom identifier instructions: %q", got)
	}
}

func TestSelectDroppedCompactionMessages(t *testing.T) {
	original := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("sys"),
		openai.UserMessage("u1"),
		openai.AssistantMessage("a1"),
		openai.UserMessage("u2"),
		openai.AssistantMessage("a2"),
	}
	compacted := []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("u2"),
		openai.AssistantMessage("a2"),
	}
	dropped := selectDroppedCompactionMessages(original, compacted, 0)
	if len(dropped) != 2 {
		t.Fatalf("expected 2 dropped user/assistant messages, got %d", len(dropped))
	}
}

func TestInjectSystemPromptAtFirstNonSystem(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("sys-1"),
		openai.SystemMessage("sys-2"),
		openai.UserMessage("hello"),
	}
	out := injectSystemPromptAtFirstNonSystem(prompt, "inserted")
	if len(out) != 4 {
		t.Fatalf("expected injected prompt length 4, got %d", len(out))
	}
	if out[2].OfSystem == nil {
		t.Fatal("expected inserted system message at first non-system boundary")
	}
}

func TestResolveCompactionSummaryModel(t *testing.T) {
	if got := resolveCompactionSummaryModel("openai/gpt-5", "openai/gpt-5.2"); got != "openai/gpt-5" {
		t.Fatalf("expected active model priority, got %q", got)
	}
	if got := resolveCompactionSummaryModel(" ", "openai/gpt-5.2"); got != "openai/gpt-5.2" {
		t.Fatalf("expected configured model fallback, got %q", got)
	}
}
