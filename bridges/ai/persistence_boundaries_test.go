package ai

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/sdk"
)

func TestSaveUserMessage_PersistsTranscriptOutsideBridgeMetadata(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portalKey := defaultChatPortalKey(client.UserLogin.ID)
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			BridgeID:  client.UserLogin.Bridge.ID,
			PortalKey: portalKey,
			Metadata:  &PortalMetadata{Slug: "chat-1"},
		},
		Bridge: client.UserLogin.Bridge,
	}
	setUnexportedField(client.UserLogin.Bridge, "portalsByKey", map[networkid.PortalKey]*bridgev2.Portal{
		portalKey: portal,
	})

	msg := &database.Message{
		ID:        "msg-1",
		Room:      portalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Timestamp: time.UnixMilli(12345),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role:              "user",
				Body:              "hello world",
				CanonicalTurnData: map[string]any{"body": "hello world"},
			},
		},
	}
	evt := &event.Event{ID: "$event-1"}

	client.saveUserMessage(ctx, evt, msg)

	bridgeMsg, err := client.loadPortalMessagePartByMXID(ctx, portal, evt.ID)
	if err != nil {
		t.Fatalf("load bridge message row: %v", err)
	}
	if bridgeMsg == nil {
		t.Fatalf("expected bridge message row")
	}
	bridgeMeta, ok := bridgeMsg.Metadata.(*MessageMetadata)
	if !ok || bridgeMeta == nil {
		t.Fatalf("expected bridge message metadata, got %#v", bridgeMsg.Metadata)
	}
	if bridgeMeta.Role != "" || bridgeMeta.Body != "" || len(bridgeMeta.CanonicalTurnData) != 0 {
		t.Fatalf("expected bridge message metadata to stay transport-only, got %#v", bridgeMeta)
	}

	transcriptMsg, err := loadAITranscriptMessage(ctx, portal, msg.ID)
	if err != nil {
		t.Fatalf("load transcript message: %v", err)
	}
	if transcriptMsg == nil {
		t.Fatalf("expected transcript message")
	}
	transcriptMeta, ok := transcriptMsg.Metadata.(*MessageMetadata)
	if !ok || transcriptMeta == nil {
		t.Fatalf("expected transcript metadata, got %#v", transcriptMsg.Metadata)
	}
	if transcriptMeta.Role != "user" || transcriptMeta.Body != "hello world" {
		t.Fatalf("expected transcript metadata to keep user payload, got %#v", transcriptMeta)
	}
	if got := transcriptMeta.CanonicalTurnData["body"]; got != "hello world" {
		t.Fatalf("expected canonical turn data to persist, got %#v", transcriptMeta.CanonicalTurnData)
	}
}

func TestSaveAIPortalState_DoesNotPersistBridgeRoomName(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			BridgeID:  client.UserLogin.Bridge.ID,
			PortalKey: defaultChatPortalKey(client.UserLogin.ID),
			Name:      "Bridge Owned Name",
		},
		Bridge: client.UserLogin.Bridge,
	}

	meta := &PortalMetadata{
		Slug:           "chat-1",
		Title:          "legacy-sidecar-title",
		TitleGenerated: true,
		WelcomeSent:    true,
	}
	portal.Metadata = meta
	if err := saveAIPortalState(ctx, portal, meta); err != nil {
		t.Fatalf("save portal state: %v", err)
	}

	loaded := &PortalMetadata{}
	loadPortalStateIntoMetadata(ctx, portal, loaded)

	if loaded.Title != "" {
		t.Fatalf("expected room title to stay out of AI sidecar state, got %q", loaded.Title)
	}
	if loaded.Slug != "chat-1" || !loaded.TitleGenerated || !loaded.WelcomeSent {
		t.Fatalf("expected AI-owned portal state to load, got %#v", loaded)
	}
}
