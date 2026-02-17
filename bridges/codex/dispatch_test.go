package codex

import (
	"encoding/json"
	"testing"
	"time"
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

// TestCodex_Dispatch_TurnCompletedNestedTurnID verifies that turn/completed
// notifications with turn ID nested in turn.id (no top-level turnId) are
// routed correctly to the subscriber.
func TestCodex_Dispatch_TurnCompletedNestedTurnID(t *testing.T) {
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

	// Simulate turn/completed with threadId at top level but turnId nested inside turn.id.
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
		if evt.Method != "turn/completed" {
			t.Fatalf("expected turn/completed, got %s", evt.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("turn/completed with nested turn.id was not routed to subscriber")
	}
}

func TestCodexExtractThreadTurn_NestedTurnID(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"threadId": "thr1",
		"turn": map[string]any{
			"id":     "turn1",
			"status": "completed",
		},
	})
	threadID, turnID, ok := codexExtractThreadTurn(params)
	if !ok {
		t.Fatal("expected ok=true for nested turn.id")
	}
	if threadID != "thr1" {
		t.Fatalf("expected threadID=thr1, got %s", threadID)
	}
	if turnID != "turn1" {
		t.Fatalf("expected turnID=turn1, got %s", turnID)
	}
}

func TestCodexExtractThreadTurn_TopLevelTurnIDTakesPrecedence(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"threadId": "thr1",
		"turnId":   "topLevel",
		"turn": map[string]any{
			"id": "nested",
		},
	})
	_, turnID, ok := codexExtractThreadTurn(params)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if turnID != "topLevel" {
		t.Fatalf("expected top-level turnId to take precedence, got %s", turnID)
	}
}
