package connector

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

func TestHistoryLimitDefaultsByRoomType(t *testing.T) {
	client := &AIClient{}

	groupPortal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!group:test", RoomType: database.RoomTypeGroupDM}}
	groupLimit := client.historyLimit(context.Background(), groupPortal, nil)
	if groupLimit != defaultGroupContextMessages {
		t.Fatalf("expected group default %d, got %d", defaultGroupContextMessages, groupLimit)
	}

	dmPortal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!dm:test", RoomType: database.RoomTypeDM}}
	dmLimit := client.historyLimit(context.Background(), dmPortal, nil)
	if dmLimit != defaultMaxContextMessages {
		t.Fatalf("expected DM default %d, got %d", defaultMaxContextMessages, dmLimit)
	}
}

func TestHistoryLimitConfigOverrides(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Messages: &MessagesConfig{
					DirectChat: &DirectChatConfig{HistoryLimit: 11},
					GroupChat:  &GroupChatConfig{HistoryLimit: 33},
				},
			},
		},
	}

	groupPortal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!group:test", RoomType: database.RoomTypeGroupDM}}
	groupLimit := client.historyLimit(context.Background(), groupPortal, nil)
	if groupLimit != 33 {
		t.Fatalf("expected group override 33, got %d", groupLimit)
	}

	dmPortal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!dm:test", RoomType: database.RoomTypeDM}}
	dmLimit := client.historyLimit(context.Background(), dmPortal, nil)
	if dmLimit != 11 {
		t.Fatalf("expected DM override 11, got %d", dmLimit)
	}
}
