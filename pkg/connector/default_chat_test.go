package connector

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestChooseDefaultChatPortalSkipsHiddenRooms(t *testing.T) {
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

	selected := chooseDefaultChatPortal([]*bridgev2.Portal{hidden, visible})
	if selected != visible {
		t.Fatalf("expected visible portal to be selected, got %#v", selected)
	}
}
