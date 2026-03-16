package opencode

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/bridges/opencode/api"
)

func TestBuildOpenCodeUserBackfillMessages(t *testing.T) {
	bridge := &Bridge{}
	msg := api.MessageWithParts{
		Info: api.Message{
			ID:        "msg-1",
			SessionID: "sess-1",
			Role:      "user",
		},
		Parts: []api.Part{
			{ID: "part-1", Type: "text", Text: "hello"},
			{ID: "part-2", Type: "reasoning", Text: "thinking"},
			{ID: "part-3", Type: "text", Text: ""},
		},
	}

	nextOrder := int64(10)
	backfill, err := bridge.buildOpenCodeUserBackfillMessages(
		context.Background(),
		&bridgev2.Portal{},
		nil,
		bridgev2.EventSender{IsFromMe: true},
		msg,
		time.Unix(1_700_000_000, 0).UTC(),
		func() int64 {
			order := nextOrder
			nextOrder++
			return order
		},
	)
	if err != nil {
		t.Fatalf("buildOpenCodeUserBackfillMessages returned error: %v", err)
	}
	if len(backfill) != 2 {
		t.Fatalf("expected 2 renderable backfill messages, got %d", len(backfill))
	}
	if backfill[0].ID != opencodePartMessageID("part-1") || backfill[1].ID != opencodePartMessageID("part-2") {
		t.Fatalf("unexpected backfill IDs: %#v", backfill)
	}
	if backfill[0].StreamOrder >= backfill[1].StreamOrder {
		t.Fatalf("expected increasing stream order, got %d then %d", backfill[0].StreamOrder, backfill[1].StreamOrder)
	}
	if backfill[0].Parts[0].Content.MsgType != event.MsgText {
		t.Fatalf("expected text message for text part, got %#v", backfill[0].Parts[0].Content)
	}
	if backfill[1].Parts[0].Content.MsgType != event.MsgNotice {
		t.Fatalf("expected notice message for reasoning part, got %#v", backfill[1].Parts[0].Content)
	}
}

func TestBuildOpenCodeSessionResync(t *testing.T) {
	session := api.Session{
		ID: "sess-1",
		Time: api.SessionTime{
			Updated: api.Timestamp(1_700_000_123_000),
			Created: api.Timestamp(1_700_000_000_000),
		},
	}

	evt := buildOpenCodeSessionResync("login-1", "instance-1", session)
	if evt == nil {
		t.Fatal("expected resync event")
	}
	if evt.GetType() != bridgev2.RemoteEventChatResync {
		t.Fatalf("unexpected event type: %v", evt.GetType())
	}
	if evt.GetPortalKey() != OpenCodePortalKey("login-1", "instance-1", "sess-1") {
		t.Fatalf("unexpected portal key: %#v", evt.GetPortalKey())
	}
	if !evt.LatestMessageTS.Equal(time.UnixMilli(1_700_000_123_000)) {
		t.Fatalf("unexpected latest message ts: %v", evt.LatestMessageTS)
	}
	if evt.GetStreamOrder() != 0 {
		t.Fatalf("unexpected stream order on resync event: %d", evt.GetStreamOrder())
	}
	if evt.GetSender() != (bridgev2.EventSender{}) {
		t.Fatalf("unexpected sender on resync event: %#v", evt.GetSender())
	}
	if evt.GetPortalKey().Receiver != networkid.UserLoginID("login-1") {
		t.Fatalf("unexpected receiver: %#v", evt.GetPortalKey())
	}
}
