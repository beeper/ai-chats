package ai

import "testing"

func TestCloneForkPortalMetadata_PreservesResolvedModelTarget(t *testing.T) {
	src := &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetModel,
			GhostID: modelUserID("openai/gpt-5"),
			ModelID: "openai/gpt-5",
		},
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
	if got.ResolvedTarget == nil || got.ResolvedTarget.Kind != ResolvedTargetModel || got.ResolvedTarget.ModelID != "openai/gpt-5" {
		t.Fatalf("expected forked metadata to keep resolved model target, got %#v", got.ResolvedTarget)
	}
}
