package aihelpers

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type testAgentCatalog struct {
	defaultAgent *Agent
	byIdentifier map[string]*Agent
}

func (c testAgentCatalog) DefaultAgent(context.Context, *bridgev2.UserLogin) (*Agent, error) {
	return c.defaultAgent, nil
}

func (c testAgentCatalog) ListAgents(context.Context, *bridgev2.UserLogin) ([]*Agent, error) {
	return nil, nil
}

func (c testAgentCatalog) ResolveAgent(_ context.Context, _ *bridgev2.UserLogin, identifier string) (*Agent, error) {
	return c.byIdentifier[identifier], nil
}

func newTestConversation(t *testing.T, cfg *Config[struct{}], state aiConversationState) *Conversation {
	t.Helper()
	store := newConversationStateStore()
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			MXID: "!room:test",
			PortalKey: networkid.PortalKey{
				ID:       "room",
				Receiver: "login",
			},
		},
	}
	conv := newConversation(
		context.Background(),
		portal,
		nil,
		bridgev2.EventSender{},
	)
	conv.store = store
	if cfg != nil {
		conv.agent = cfg.Agent
		conv.agentCatalog = cfg.AgentCatalog
		conv.roomFeatures = cfg.RoomFeatures
	}
	if err := conv.saveState(context.Background(), &state); err != nil {
		t.Fatalf("saveState failed: %v", err)
	}
	return conv
}

func TestConversationCurrentRoomFeaturesUsesConfiguredDefaultAgent(t *testing.T) {
	conv := newTestConversation(t, &Config[struct{}]{
		Agent: &Agent{
			ID: "default",
			Capabilities: AgentCapabilities{
				SupportsImageInput: true,
				MaxTextLength:      64000,
			},
		},
	}, aiConversationState{})

	features := conv.currentRoomFeatures(context.Background())
	if !features.SupportsImages {
		t.Fatalf("expected image support from configured default agent")
	}
	if features.MaxTextLength != 64000 {
		t.Fatalf("expected default agent text length 64000, got %d", features.MaxTextLength)
	}
}

func TestConversationCurrentRoomFeaturesFallsBackAfterUnresolvedAgents(t *testing.T) {
	conv := newTestConversation(t, &Config[struct{}]{
		Agent: &Agent{
			ID: "default",
			Capabilities: AgentCapabilities{
				SupportsFileInput: true,
				MaxTextLength:     32000,
			},
		},
	}, aiConversationState{
		RoomAgents: RoomAgentSet{AgentIDs: []string{"missing-a", "missing-b"}},
	})

	features := conv.currentRoomFeatures(context.Background())
	if !features.SupportsFiles {
		t.Fatalf("expected file support from fallback default agent")
	}
	if features.MaxTextLength != 32000 {
		t.Fatalf("expected fallback agent text length 32000, got %d", features.MaxTextLength)
	}
}

func TestConversationCurrentRoomFeaturesIgnoresUnresolvedAgentsWhenOneResolves(t *testing.T) {
	conv := newTestConversation(t, &Config[struct{}]{
		AgentCatalog: testAgentCatalog{
			byIdentifier: map[string]*Agent{
				"found": {
					ID: "found",
					Capabilities: AgentCapabilities{
						SupportsStreaming:  true,
						SupportsAudioInput: true,
						MaxTextLength:      48000,
					},
				},
			},
		},
	}, aiConversationState{
		RoomAgents: RoomAgentSet{AgentIDs: []string{"missing", "found"}},
	})

	features := conv.currentRoomFeatures(context.Background())
	if !features.SupportsAudio {
		t.Fatalf("expected audio support from resolved room agent")
	}
	if !features.SupportsTyping {
		t.Fatalf("expected typing support from resolved room agent")
	}
	if features.MaxTextLength != 48000 {
		t.Fatalf("expected resolved agent text length 48000, got %d", features.MaxTextLength)
	}
}
