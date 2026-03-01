package connector

import "testing"

func TestCloneForkPortalMetadata_PreservesSimpleMode(t *testing.T) {
	src := &PortalMetadata{
		Model:               "openai/gpt-5",
		SystemPrompt:        "You are helpful.",
		Temperature:         0.3,
		MaxContextMessages:  42,
		MaxCompletionTokens: 2048,
		ReasoningEffort:     "medium",
		Capabilities: ModelCapabilities{
			SupportsToolCalling: true,
		},
		ConversationMode: "responses",
		AgentID:          "beeper",
		AgentPrompt:      "agent prompt",
		IsSimpleMode:     true,
		GroupActivation:  "always", // Not copied in fork metadata.
	}

	got := cloneForkPortalMetadata(src, "chat-99", "Forked Chat")
	if got == nil {
		t.Fatalf("expected cloned metadata")
	}
	if got.Slug != "chat-99" {
		t.Fatalf("expected slug chat-99, got %q", got.Slug)
	}
	if got.Title != "Forked Chat" {
		t.Fatalf("expected title Forked Chat, got %q", got.Title)
	}
	if !got.IsSimpleMode {
		t.Fatalf("expected IsSimpleMode=true on forked metadata")
	}
	if got.GroupActivation != "" {
		t.Fatalf("expected GroupActivation to remain unset in fork metadata copy, got %q", got.GroupActivation)
	}
}
