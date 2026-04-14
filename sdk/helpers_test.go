package sdk

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type testMessageMetadata struct {
	Revision string `json:"revision,omitempty"`
}

func newTestBridgeDBWithMessageMeta(t *testing.T) *database.Database {
	t.Helper()
	raw, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })

	db, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		t.Fatalf("wrap db: %v", err)
	}
	bridgeDB := database.New(networkid.BridgeID("bridge"), database.MetaTypes{
		Message: func() any { return &testMessageMetadata{} },
	}, db)
	if err = bridgeDB.Upgrade(context.Background()); err != nil {
		t.Fatalf("upgrade bridge db: %v", err)
	}
	return bridgeDB
}

func TestApplyAgentRemoteBridgeInfoRoomTypes(t *testing.T) {
	cases := []struct {
		name     string
		roomType database.RoomType
		aiKind   string
		want     string
	}{
		{name: "agent dm", roomType: database.RoomTypeDM, aiKind: AIRoomKindAgent, want: "dm"},
		{name: "agent default", roomType: database.RoomTypeDefault, aiKind: AIRoomKindAgent, want: "group"},
		{name: "agent space", roomType: database.RoomTypeSpace, aiKind: AIRoomKindAgent, want: "space"},
		{name: "subagent forced group", roomType: database.RoomTypeDM, aiKind: "subagent", want: "group"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := &event.BridgeEventContent{}
			ApplyAgentRemoteBridgeInfo(content, "", tc.roomType, tc.aiKind)
			if content.BeeperRoomTypeV2 != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, content.BeeperRoomTypeV2)
			}
		})
	}
}

func TestApplyAgentRemoteBridgeInfo(t *testing.T) {
	content := &event.BridgeEventContent{}
	ApplyAgentRemoteBridgeInfo(content, "ai-codex", database.RoomTypeDM, AIRoomKindAgent)

	if content.Protocol.ID != "ai-codex" {
		t.Fatalf("expected protocol id ai-codex, got %q", content.Protocol.ID)
	}
	if content.BeeperRoomTypeV2 != "dm" {
		t.Fatalf("expected dm room type, got %q", content.BeeperRoomTypeV2)
	}
}

func TestNewTurnIDIsOpaqueUUID(t *testing.T) {
	turnID := NewTurnID()
	otherID := NewTurnID()
	if turnID == "" || otherID == "" {
		t.Fatal("expected non-empty turn id")
	}
	if strings.HasPrefix(turnID, "turn_") {
		t.Fatalf("expected opaque turn id, got legacy prefix %q", turnID)
	}
	if turnID == otherID {
		t.Fatalf("expected unique turn ids, got %q twice", turnID)
	}
	if _, err := uuid.Parse(turnID); err != nil {
		t.Fatalf("expected uuid-shaped turn id, got %q: %v", turnID, err)
	}
}

func TestUpsertAssistantMessageUsesStrictNetworkIDLookup(t *testing.T) {
	db := newTestBridgeDBWithMessageMeta(t)
	bridge := &bridgev2.Bridge{DB: db}
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID: "login-1",
		},
		Bridge: bridge,
	}
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: networkid.PortalKey{
				ID:       "portal-1",
				Receiver: login.ID,
			},
			MXID: id.RoomID("!room:test"),
		},
		Bridge: bridge,
	}

	ctx := context.Background()
	UpsertAssistantMessage(ctx, UpsertAssistantMessageParams{
		Login:            login,
		Portal:           portal,
		SenderID:         networkid.UserID("@ghost:test"),
		NetworkMessageID: networkid.MessageID("msg-1"),
		InitialEventID:   id.EventID("$event-1"),
		Metadata: map[string]any{
			"revision": "one",
		},
		Logger: zerolog.Nop(),
	})

	msg, err := db.Message.GetPartByID(ctx, login.ID, networkid.MessageID("msg-1"), networkid.PartID("0"))
	if err != nil {
		t.Fatalf("expected inserted assistant message, got error: %v", err)
	}
	if msg == nil {
		t.Fatal("expected assistant message row to be inserted")
	}
	if msg.MXID != id.EventID("$event-1") {
		t.Fatalf("expected mxid to preserve initial event id, got %q", msg.MXID)
	}
	metadata, ok := msg.Metadata.(*testMessageMetadata)
	if !ok || metadata.Revision != "one" {
		t.Fatalf("expected metadata to persist, got %#v", msg.Metadata)
	}

	UpsertAssistantMessage(ctx, UpsertAssistantMessageParams{
		Login:            login,
		Portal:           portal,
		SenderID:         networkid.UserID("@ghost:test"),
		NetworkMessageID: networkid.MessageID("msg-1"),
		InitialEventID:   id.EventID("$event-1"),
		Metadata: map[string]any{
			"revision": "two",
		},
		Logger: zerolog.Nop(),
	})

	updated, err := db.Message.GetPartByID(ctx, login.ID, networkid.MessageID("msg-1"), networkid.PartID("0"))
	if err != nil {
		t.Fatalf("expected updated assistant message, got error: %v", err)
	}
	updatedMetadata, ok := updated.Metadata.(*testMessageMetadata)
	if !ok || updatedMetadata.Revision != "two" {
		t.Fatalf("expected strict update by network id, got %#v", updated.Metadata)
	}
}

func TestUpsertAssistantMessageRequiresCanonicalIdentifiers(t *testing.T) {
	db := newTestBridgeDB(t)
	bridge := &bridgev2.Bridge{DB: db}
	login := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID: "login-1",
		},
		Bridge: bridge,
	}
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: networkid.PortalKey{
				ID:       "portal-1",
				Receiver: login.ID,
			},
			MXID: id.RoomID("!room:test"),
		},
		Bridge: bridge,
	}

	UpsertAssistantMessage(context.Background(), UpsertAssistantMessageParams{
		Login:          login,
		Portal:         portal,
		SenderID:       networkid.UserID("@ghost:test"),
		InitialEventID: id.EventID("$event-1"),
		Metadata:       map[string]any{"revision": "one"},
		Logger:         zerolog.Nop(),
	})

	msg, err := db.Message.GetPartByMXID(context.Background(), id.EventID("$event-1"))
	if err != nil {
		t.Fatalf("expected no-op to avoid lookup error, got %v", err)
	}
	if msg != nil {
		t.Fatalf("expected no row when network message id is missing, got %#v", msg)
	}
}
