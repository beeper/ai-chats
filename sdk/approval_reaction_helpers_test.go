package sdk

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
	"maunium.net/go/mautrix/event"
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

func TestPreHandleApprovalReaction_LeavesSenderUnassigned(t *testing.T) {
	msg := &bridgev2.MatrixReaction{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{
			Event: &event.Event{
				ID:     id.EventID("$reaction"),
				Sender: id.UserID("@owner:example.com"),
			},
			Content: &event.ReactionEventContent{
				RelatesTo: event.RelatesTo{
					Type:    event.RelAnnotation,
					EventID: id.EventID("$target"),
					Key:     ApprovalReactionKeyAllowOnce,
				},
			},
		},
	}

	preResp, err := PreHandleApprovalReaction(msg)
	if err != nil {
		t.Fatalf("PreHandleApprovalReaction failed: %v", err)
	}
	if preResp.SenderID != "" {
		t.Fatalf("expected empty sender id, got %q", preResp.SenderID)
	}
	if preResp.Emoji != ApprovalReactionKeyAllowOnce {
		t.Fatalf("expected normalized emoji %q, got %q", ApprovalReactionKeyAllowOnce, preResp.Emoji)
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
