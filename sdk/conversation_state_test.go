package sdk

import "testing"

func TestNormalizeConversationSpecDelegatedDefaults(t *testing.T) {
	spec := normalizeConversationSpec(ConversationSpec{
		Kind:     ConversationKindDelegated,
		PortalID: "child-1",
	})
	if spec.Visibility != ConversationVisibilityHidden {
		t.Fatalf("expected delegated visibility to default hidden, got %q", spec.Visibility)
	}
	if !spec.ArchiveOnCompletion {
		t.Fatalf("expected delegated conversations to default archive-on-completion")
	}
}

func TestConversationStateRoundTripGenericMetadata(t *testing.T) {
	meta := map[string]any{}
	holder := any(&meta)
	state := &sdkConversationState{
		Kind:                 ConversationKindDelegated,
		Visibility:           ConversationVisibilityHidden,
		ParentConversationID: "!parent:example.com",
		ParentEventID:        "$event",
		ArchiveOnCompletion:  true,
		Metadata:             map[string]any{"label": "child"},
		RoomAgents: RoomAgentSet{
			PrimaryAgentID: "agent-a",
			AgentIDs:       []string{"agent-a", "agent-a", "agent-b"},
		},
	}
	if ok := saveConversationStateToGenericMetadata(&holder, state); !ok {
		t.Fatalf("expected generic metadata save to succeed")
	}
	loaded, ok := loadConversationStateFromGenericMetadata(holder)
	if !ok || loaded == nil {
		t.Fatalf("expected generic metadata load to succeed")
	}
	loaded.ensureDefaults()
	if loaded.Kind != ConversationKindDelegated {
		t.Fatalf("expected delegated kind, got %q", loaded.Kind)
	}
	if loaded.Visibility != ConversationVisibilityHidden {
		t.Fatalf("expected hidden visibility, got %q", loaded.Visibility)
	}
	if loaded.ParentConversationID != "!parent:example.com" {
		t.Fatalf("unexpected parent conversation id %q", loaded.ParentConversationID)
	}
	if len(loaded.RoomAgents.AgentIDs) != 2 {
		t.Fatalf("expected deduped agent ids, got %v", loaded.RoomAgents.AgentIDs)
	}
	if loaded.RoomAgents.PrimaryAgentID != "agent-a" {
		t.Fatalf("unexpected primary agent %q", loaded.RoomAgents.PrimaryAgentID)
	}
}
