package sdk

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func setupConversationStateTestPortal(t *testing.T, receiver networkid.UserLoginID, portalID networkid.PortalID) *bridgev2.Portal {
	t.Helper()
	bridgeDB := newTestBridgeDB(t)
	if _, err := bridgeDB.Database.Exec(context.Background(), `
		CREATE TABLE sdk_conversation_state (
			bridge_id TEXT NOT NULL,
			login_id TEXT NOT NULL,
			portal_id TEXT NOT NULL,
			state_json TEXT NOT NULL DEFAULT '',
			updated_at_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bridge_id, login_id, portal_id)
		)
	`); err != nil {
		t.Fatalf("create sdk conversation state table: %v", err)
	}
	return &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: networkid.PortalKey{
				ID:       portalID,
				Receiver: receiver,
			},
			MXID: id.RoomID("!room:test"),
		},
		Bridge: &bridgev2.Bridge{DB: bridgeDB},
	}
}

func TestConversationStateSaveAndLoadUsesBridgeDB(t *testing.T) {
	portal := setupConversationStateTestPortal(t, "login-a", "room-a")
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

	store := newConversationStateStore()
	if err := saveConversationState(context.Background(), portal, store, state); err != nil {
		t.Fatalf("saveConversationState failed: %v", err)
	}
	if portal.Metadata != nil {
		t.Fatalf("expected portal metadata to remain untouched, got %#v", portal.Metadata)
	}

	loaded, err := loadConversationStateFromDB(context.Background(), portal)
	if err != nil {
		t.Fatalf("loadConversationStateFromDB failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected DB-backed state to load")
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
	if loaded.RoomAgents.AgentIDs[0] != "agent-a" || loaded.RoomAgents.AgentIDs[1] != "agent-b" {
		t.Fatalf("unexpected agent order after normalization: %v", loaded.RoomAgents.AgentIDs)
	}
}

func TestConversationStateLoadFallsBackToDBWhenCacheMisses(t *testing.T) {
	portal := setupConversationStateTestPortal(t, "login-b", "room-b")
	state := &sdkConversationState{
		Kind:                ConversationKindNormal,
		ArchiveOnCompletion: true,
		RoomAgents: RoomAgentSet{
			AgentIDs: []string{"agent-c"},
		},
	}

	if err := saveConversationState(context.Background(), portal, newConversationStateStore(), state); err != nil {
		t.Fatalf("saveConversationState failed: %v", err)
	}

	loaded := loadConversationState(portal, newConversationStateStore())
	if loaded == nil {
		t.Fatal("expected loaded state")
	}
	if !loaded.ArchiveOnCompletion {
		t.Fatal("expected archive-on-completion to round-trip")
	}
	if len(loaded.RoomAgents.AgentIDs) != 1 || loaded.RoomAgents.AgentIDs[0] != "agent-c" {
		t.Fatalf("unexpected agent ids: %v", loaded.RoomAgents.AgentIDs)
	}
}
