package ai

import "testing"

func TestFilterPromptBlocksForHistoryDropsThinking(t *testing.T) {
	filtered := filterPromptBlocksForHistory([]PromptBlock{
		{Type: PromptBlockThinking, Text: "internal analysis"},
		{Type: PromptBlockText, Text: "visible reply"},
	}, false)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 block after filtering, got %d", len(filtered))
	}
	if filtered[0].Type != PromptBlockText || filtered[0].Text != "visible reply" {
		t.Fatalf("unexpected filtered blocks: %#v", filtered)
	}
}

func TestPromptMessageVisibleTextOmitsThinking(t *testing.T) {
	msg := PromptMessage{
		Role: PromptRoleAssistant,
		Blocks: []PromptBlock{
			{Type: PromptBlockThinking, Text: "internal analysis"},
			{Type: PromptBlockText, Text: "visible reply"},
		},
	}
	if got := msg.VisibleText(); got != "visible reply" {
		t.Fatalf("expected visible text only, got %q", got)
	}
	if got := msg.Text(); got != "internal analysis\nvisible reply" {
		t.Fatalf("expected full text to retain thinking, got %q", got)
	}
}
