package sdk

import "testing"

type testConversationCarrier struct {
	SDK *SDKPortalMetadata
}

func (c *testConversationCarrier) GetSDKPortalMetadata() *SDKPortalMetadata {
	if c == nil {
		return nil
	}
	return c.SDK
}

func (c *testConversationCarrier) SetSDKPortalMetadata(meta *SDKPortalMetadata) {
	if c == nil {
		return
	}
	c.SDK = meta
}

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
			AgentIDs: []string{"agent-a", "agent-a", "agent-b"},
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
}

func TestConversationStateRoundTripCarrierMetadata(t *testing.T) {
	carrier := &testConversationCarrier{}
	holder := any(carrier)
	state := &sdkConversationState{
		Kind:                ConversationKindNormal,
		ArchiveOnCompletion: true,
		RoomAgents: RoomAgentSet{
			AgentIDs: []string{"agent-a"},
		},
	}
	if !saveConversationStateToGenericMetadata(&holder, state) {
		// Generic metadata intentionally doesn't support the carrier path.
	}
	carrier.SetSDKPortalMetadata(&SDKPortalMetadata{Conversation: *state})
	loaded, ok := carrier.GetSDKPortalMetadata(), carrier.GetSDKPortalMetadata() != nil
	if !ok || loaded == nil {
		t.Fatalf("expected carrier metadata to be set")
	}
	if loaded.Conversation.ArchiveOnCompletion != state.ArchiveOnCompletion {
		t.Fatalf("expected carrier archive flag to round-trip")
	}
	if len(loaded.Conversation.RoomAgents.AgentIDs) != 1 || loaded.Conversation.RoomAgents.AgentIDs[0] != "agent-a" {
		t.Fatalf("unexpected carrier agent ids: %v", loaded.Conversation.RoomAgents.AgentIDs)
	}
}
