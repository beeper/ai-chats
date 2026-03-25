package agentremote

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func setupApprovalReactionTestLogin(t *testing.T) *bridgev2.UserLogin {
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
	bridgeDB := database.New(networkid.BridgeID("bridge"), database.MetaTypes{}, db)
	if err = bridgeDB.Upgrade(context.Background()); err != nil {
		t.Fatalf("upgrade bridge db: %v", err)
	}

	return &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{ID: networkid.UserLoginID("login")},
		Bridge:    &bridgev2.Bridge{DB: bridgeDB},
	}
}

func TestEnsureSyntheticReactionSenderGhost_CreatesGhostRow(t *testing.T) {
	login := setupApprovalReactionTestLogin(t)
	ctx := context.Background()
	userMXID := id.UserID("@owner:example.com")
	senderID := MatrixSenderID(userMXID)

	if err := EnsureSyntheticReactionSenderGhost(ctx, login, userMXID); err != nil {
		t.Fatalf("EnsureSyntheticReactionSenderGhost failed: %v", err)
	}
	if err := EnsureSyntheticReactionSenderGhost(ctx, login, userMXID); err != nil {
		t.Fatalf("EnsureSyntheticReactionSenderGhost should be idempotent: %v", err)
	}

	ghost, err := login.Bridge.DB.Ghost.GetByID(ctx, senderID)
	if err != nil {
		t.Fatalf("query ghost: %v", err)
	}
	if ghost == nil {
		t.Fatalf("expected synthetic ghost row for %q", senderID)
	}
	if ghost.ID != senderID {
		t.Fatalf("expected ghost id %q, got %q", senderID, ghost.ID)
	}
}

func TestResolveApprovalReactionTargetMessageID_UsesReplyTargetEvent(t *testing.T) {
	login := setupApprovalReactionTestLogin(t)
	ctx := context.Background()

	err := login.Bridge.DB.Message.Insert(ctx, &database.Message{
		ID:         networkid.MessageID("assistant-msg"),
		PartID:     networkid.PartID("0"),
		MXID:       id.EventID("$assistant"),
		Room:       networkid.PortalKey{ID: networkid.PortalID("portal"), Receiver: login.ID},
		SenderID:   networkid.UserID("ghost:assistant"),
		SenderMXID: id.UserID("@assistant:example.com"),
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}

	got := resolveApprovalReactionTargetMessageID(ctx, login, id.EventID("$assistant"))
	if got != networkid.MessageID("assistant-msg") {
		t.Fatalf("expected assistant target message id, got %q", got)
	}
}
