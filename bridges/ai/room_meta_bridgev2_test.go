package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestHandleMatrixRoomName_PersistsViaBridgev2Portal(t *testing.T) {
	ctx := context.Background()
	client, portal := newBridgev2RoomMetaTestPortal(t)

	portal.Name = "Bridge Owned Name"
	portal.NameSet = true

	changed, err := client.HandleMatrixRoomName(ctx, &bridgev2.MatrixRoomName{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.RoomNameEventContent]{Portal: portal},
	})
	if err != nil {
		t.Fatalf("handle room name: %v", err)
	}
	if !changed {
		t.Fatal("expected room name handler to accept bridgev2-owned name changes")
	}
	if err = portal.Save(ctx); err != nil {
		t.Fatalf("save portal: %v", err)
	}

	stored, err := client.UserLogin.Bridge.DB.Portal.GetByKey(ctx, portal.PortalKey)
	if err != nil {
		t.Fatalf("load stored portal: %v", err)
	}
	if stored == nil {
		t.Fatal("expected stored portal row")
	}
	if stored.Name != "Bridge Owned Name" || !stored.NameSet {
		t.Fatalf("expected bridge portal row to persist room name, got %#v", stored)
	}
}

func TestHandleMatrixRoomTopic_PersistsViaBridgev2Portal(t *testing.T) {
	ctx := context.Background()
	client, portal := newBridgev2RoomMetaTestPortal(t)

	portal.Topic = "Bridge Owned Topic"
	portal.TopicSet = true

	changed, err := client.HandleMatrixRoomTopic(ctx, &bridgev2.MatrixRoomTopic{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.TopicEventContent]{Portal: portal},
	})
	if err != nil {
		t.Fatalf("handle room topic: %v", err)
	}
	if !changed {
		t.Fatal("expected room topic handler to accept bridgev2-owned topic changes")
	}
	if err = portal.Save(ctx); err != nil {
		t.Fatalf("save portal: %v", err)
	}

	stored, err := client.UserLogin.Bridge.DB.Portal.GetByKey(ctx, portal.PortalKey)
	if err != nil {
		t.Fatalf("load stored portal: %v", err)
	}
	if stored == nil {
		t.Fatal("expected stored portal row")
	}
	if stored.Topic != "Bridge Owned Topic" || !stored.TopicSet {
		t.Fatalf("expected bridge portal row to persist room topic, got %#v", stored)
	}
}

func TestHandleMatrixRoomAvatar_PersistsViaBridgev2Portal(t *testing.T) {
	ctx := context.Background()
	client, portal := newBridgev2RoomMetaTestPortal(t)

	portal.AvatarMXC = id.ContentURIString("mxc://example.com/avatar")
	portal.AvatarSet = true

	changed, err := client.HandleMatrixRoomAvatar(ctx, &bridgev2.MatrixRoomAvatar{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.RoomAvatarEventContent]{Portal: portal},
	})
	if err != nil {
		t.Fatalf("handle room avatar: %v", err)
	}
	if !changed {
		t.Fatal("expected room avatar handler to accept bridgev2-owned avatar changes")
	}
	if err = portal.Save(ctx); err != nil {
		t.Fatalf("save portal: %v", err)
	}

	stored, err := client.UserLogin.Bridge.DB.Portal.GetByKey(ctx, portal.PortalKey)
	if err != nil {
		t.Fatalf("load stored portal: %v", err)
	}
	if stored == nil {
		t.Fatal("expected stored portal row")
	}
	if stored.AvatarMXC != "mxc://example.com/avatar" || !stored.AvatarSet {
		t.Fatalf("expected bridge portal row to persist room avatar, got %#v", stored)
	}
}

func newBridgev2RoomMetaTestPortal(t *testing.T) (*AIClient, *bridgev2.Portal) {
	t.Helper()

	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			BridgeID: client.UserLogin.Bridge.ID,
			PortalKey: networkid.PortalKey{
				ID:       networkid.PortalID("chat-room-meta"),
				Receiver: client.UserLogin.ID,
			},
			MXID:     id.RoomID("!room-meta:example.com"),
			Metadata: &PortalMetadata{Slug: "chat-room-meta"},
		},
		Bridge: client.UserLogin.Bridge,
	}
	if err := client.UserLogin.Bridge.DB.Portal.Insert(ctx, portal.Portal); err != nil {
		t.Fatalf("insert portal: %v", err)
	}
	return client, portal
}
