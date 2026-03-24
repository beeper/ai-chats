package ai

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestChooseDefaultChatPortalSkipsHiddenRooms(t *testing.T) {
	client := &AIClient{}
	hidden := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: networkid.PortalKey{ID: "openai:hidden"},
			Metadata: &PortalMetadata{
				Slug:       "chat-1",
				ModuleMeta: map[string]any{"cron": map[string]any{"is_internal_room": true}},
			},
		},
	}
	visible := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: networkid.PortalKey{ID: "openai:visible"},
			Metadata: &PortalMetadata{
				Slug: "chat-2",
			},
		},
	}

	selected := client.chooseDefaultChatPortal([]*bridgev2.Portal{hidden, visible})
	if selected != visible {
		t.Fatalf("expected visible portal to be selected, got %#v", selected)
	}
}

func TestChooseDefaultChatPortalDisabledSkipsAgentRooms(t *testing.T) {
	disabled := false
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Agents: &AgentsConfig{Enabled: &disabled},
			},
		},
	}
	agentPortal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey:   networkid.PortalKey{ID: "openai:agent"},
			OtherUserID: agentUserID("beeper"),
			Metadata:    agentModeTestMeta("beeper"),
		},
	}
	modelPortal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey:   networkid.PortalKey{ID: "openai:model"},
			OtherUserID: modelUserID("openai/gpt-5"),
			Metadata:    simpleModeTestMeta("openai/gpt-5"),
		},
	}

	selected := client.chooseDefaultChatPortal([]*bridgev2.Portal{agentPortal, modelPortal})
	if selected != modelPortal {
		t.Fatalf("expected model portal to be selected when agents are disabled, got %#v", selected)
	}
}
