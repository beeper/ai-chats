package ai

import "testing"

func TestFilterPromptMessagesForHistoryDropsThinking(t *testing.T) {
	filtered := filterPromptMessagesForHistory([]PromptMessage{{
		Role: PromptRoleAssistant,
		Blocks: []PromptBlock{
			{Type: PromptBlockThinking, Text: "internal analysis"},
			{Type: PromptBlockText, Text: "visible reply"},
		},
	}}, false)
	if len(filtered) != 1 || len(filtered[0].Blocks) != 1 {
		t.Fatalf("expected one visible prompt block after filtering, got %#v", filtered)
	}
	if filtered[0].Blocks[0].Type != PromptBlockText || filtered[0].Blocks[0].Text != "visible reply" {
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
