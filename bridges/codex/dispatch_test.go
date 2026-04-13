package codex

import (
	"encoding/json"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func TestCodex_Dispatch_RoutesByThreadTurn(t *testing.T) {
	cc := &CodexClient{
		notifCh:       make(chan codexNotif, 16),
		turnSubs:      make(map[string]chan codexNotif),
		activeTurns:   make(map[string]*codexActiveTurn),
		loadedThreads: make(map[string]bool),
	}
	go cc.dispatchNotifications()

	ch1 := cc.subscribeTurn("thr1", "turn1")
	ch2 := cc.subscribeTurn("thr2", "turn2")
	defer cc.unsubscribeTurn("thr1", "turn1")
	defer cc.unsubscribeTurn("thr2", "turn2")

	p1, _ := json.Marshal(map[string]any{"threadId": "thr1", "turnId": "turn1", "delta": "a"})
	p2, _ := json.Marshal(map[string]any{"threadId": "thr2", "turnId": "turn2", "delta": "b"})

	cc.notifCh <- codexNotif{Method: "item/agentMessage/delta", Params: p1}
	cc.notifCh <- codexNotif{Method: "item/agentMessage/delta", Params: p2}

	// Each channel should receive only its own event.
	select {
	case evt := <-ch1:
		if evt.Method != "item/agentMessage/delta" {
			t.Fatalf("unexpected evt on ch1: %+v", evt)
		}
		var p map[string]any
		_ = json.Unmarshal(evt.Params, &p)
		if p["threadId"] != "thr1" || p["turnId"] != "turn1" {
			t.Fatalf("misrouted to ch1: %v", p)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for ch1")
	}

	select {
	case evt := <-ch2:
		if evt.Method != "item/agentMessage/delta" {
			t.Fatalf("unexpected evt on ch2: %+v", evt)
		}
		var p map[string]any
		_ = json.Unmarshal(evt.Params, &p)
		if p["threadId"] != "thr2" || p["turnId"] != "turn2" {
			t.Fatalf("misrouted to ch2: %v", p)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for ch2")
	}
}

func TestCodexExtractThreadTurn_TopLevelTurnIDRequired(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"threadId": "thr1",
		"turnId":   "topLevel",
	})
	_, turnID, ok := codexExtractThreadTurn(params)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if turnID != "topLevel" {
		t.Fatalf("expected top-level turnId, got %s", turnID)
	}
}

func TestCodexExtractThreadTurn_RejectsMissingTopLevelTurnID(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"threadId": "thr1",
		"turn": map[string]any{
			"id":     "nestedTurn",
			"status": "completed",
		},
	})
	threadID, turnID, ok := codexExtractThreadTurn(params)
	if ok {
		t.Fatalf("expected strict extraction to fail, got thread=%q turn=%q", threadID, turnID)
	}
}

func TestCodex_Dispatch_DropsTurnCompletedWithoutTopLevelTurnID(t *testing.T) {
	cc := &CodexClient{
		notifCh:       make(chan codexNotif, 16),
		notifDone:     make(chan struct{}),
		turnSubs:      make(map[string]chan codexNotif),
		activeTurns:   make(map[string]*codexActiveTurn),
		loadedThreads: make(map[string]bool),
	}
	go cc.dispatchNotifications()
	defer close(cc.notifDone)

	ch := cc.subscribeTurn("thr1", "turn1")
	defer cc.unsubscribeTurn("thr1", "turn1")

	params, _ := json.Marshal(map[string]any{
		"threadId": "thr1",
		"turn": map[string]any{
			"id":     "turn1",
			"status": "completed",
		},
	})

	cc.notifCh <- codexNotif{Method: "turn/completed", Params: params}

	select {
	case evt := <-ch:
		t.Fatalf("expected no routed event, got %+v", evt)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCodexRestoreRecoveredActiveTurns_RegistersInProgressTurns(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}
	state := &codexPortalState{CodexThreadID: "thr1"}
	cc := &CodexClient{
		activeTurns: make(map[string]*codexActiveTurn),
	}

	cc.restoreRecoveredActiveTurns(portal, state, codexThread{
		ID: "thr1",
		Turns: []codexTurn{
			{ID: "turn-active", Status: "inProgress"},
			{ID: "turn-done", Status: "completed"},
		},
	}, "gpt-5.1-codex")

	active := cc.activeTurns[codexTurnKey("thr1", "turn-active")]
	if active == nil {
		t.Fatal("expected in-progress turn to be restored")
	}
	if active.streamState == nil || active.streamState.turnID != "turn-active" {
		t.Fatalf("expected recovered streaming state for active turn, got %#v", active.streamState)
	}
	if _, ok := cc.activeTurns[codexTurnKey("thr1", "turn-done")]; ok {
		t.Fatal("did not expect completed turn to be restored")
	}
}
