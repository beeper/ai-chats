package turns

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func TestStreamSessionEmitPartUsesResolvedRelationTarget(t *testing.T) {
	t.Helper()

	var gotContent map[string]any
	session := NewStreamSession(StreamSessionParams{
		TurnID:  "turn-1",
		AgentID: "agent-1",
		GetStreamTarget: func() StreamTarget {
			return StreamTarget{NetworkMessageID: networkid.MessageID("msg-1")}
		},
		ResolveTargetEventID: func(context.Context, StreamTarget) (id.EventID, error) {
			return id.EventID("$event-1"), nil
		},
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:example.com")
		},
		NextSeq: func() int { return 1 },
		SendHook: func(_ string, _ int, content map[string]any, _ string) bool {
			gotContent = content
			return true
		},
	})

	session.EmitPart(context.Background(), map[string]any{"type": "text-delta", "delta": "hello"})

	if gotContent == nil {
		t.Fatal("expected stream content to be emitted")
	}
	relatesTo, ok := gotContent["m.relates_to"].(map[string]any)
	if !ok {
		t.Fatalf("expected m.relates_to, got %#v", gotContent)
	}
	if relatesTo["event_id"] != "$event-1" {
		t.Fatalf("unexpected relation target: %#v", relatesTo)
	}
}

func TestStreamSessionFallsBackToDebouncedWithoutResolvedEventID(t *testing.T) {
	t.Helper()

	debounced := make(chan struct{}, 1)
	session := NewStreamSession(StreamSessionParams{
		TurnID: "turn-2",
		GetStreamTarget: func() StreamTarget {
			return StreamTarget{NetworkMessageID: networkid.MessageID("msg-2")}
		},
		ResolveTargetEventID: func(context.Context, StreamTarget) (id.EventID, error) {
			return "", nil
		},
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:example.com")
		},
		NextSeq: func() int { return 1 },
		SendDebouncedEdit: func(context.Context, bool) error {
			debounced <- struct{}{}
			return nil
		},
		SendHook: func(_ string, _ int, _ map[string]any, _ string) bool {
			t.Fatal("did not expect hook send when target event is unresolved")
			return false
		},
	})
	defer session.End(context.Background(), EndReasonFinish)

	session.EmitPart(context.Background(), map[string]any{"type": "finish"})

	select {
	case <-debounced:
	case <-time.After(2 * time.Second):
		t.Fatal("expected debounced fallback send")
	}
}

func TestStreamSessionDoesNothingWithoutEditTarget(t *testing.T) {
	t.Helper()

	called := make(chan struct{}, 1)
	session := NewStreamSession(StreamSessionParams{
		TurnID: "turn-3",
		GetStreamTarget: func() StreamTarget {
			return StreamTarget{}
		},
		ResolveTargetEventID: func(context.Context, StreamTarget) (id.EventID, error) {
			t.Fatal("did not expect target resolution without an edit target")
			return "", nil
		},
		SendDebouncedEdit: func(context.Context, bool) error {
			called <- struct{}{}
			return nil
		},
		SendHook: func(_ string, _ int, _ map[string]any, _ string) bool {
			called <- struct{}{}
			return true
		},
	})
	defer session.End(context.Background(), EndReasonFinish)

	session.EmitPart(context.Background(), map[string]any{"type": "finish"})

	select {
	case <-called:
		t.Fatal("did not expect stream send without an edit target")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestStreamSessionApprovalRequestPersistsCheckpointWithoutFallback(t *testing.T) {
	t.Helper()

	var fallback atomic.Bool
	hookCalled := make(chan struct{}, 1)
	debouncedForce := make(chan bool, 1)

	session := NewStreamSession(StreamSessionParams{
		TurnID:  "turn-4",
		AgentID: "agent-1",
		GetStreamTarget: func() StreamTarget {
			return StreamTarget{NetworkMessageID: networkid.MessageID("msg-4")}
		},
		ResolveTargetEventID: func(context.Context, StreamTarget) (id.EventID, error) {
			return id.EventID("$event-4"), nil
		},
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:example.com")
		},
		NextSeq:             func() int { return 1 },
		RuntimeFallbackFlag: &fallback,
		SendDebouncedEdit: func(_ context.Context, force bool) error {
			debouncedForce <- force
			return nil
		},
		SendHook: func(_ string, _ int, _ map[string]any, _ string) bool {
			hookCalled <- struct{}{}
			return true
		},
	})

	session.EmitPart(context.Background(), map[string]any{
		"type":       "tool-approval-request",
		"approvalId": "approval-1",
		"toolCallId": "tool-call-1",
	})

	select {
	case <-hookCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected approval request to be streamed")
	}

	select {
	case force := <-debouncedForce:
		if !force {
			t.Fatal("expected approval checkpoint edit to be forced")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected approval request to trigger a persisted checkpoint edit")
	}

	if fallback.Load() {
		t.Fatal("did not expect approval checkpoint edit to switch stream transport into fallback mode")
	}
}

func TestStreamSessionApprovalResponsePersistsCheckpointWithoutFallback(t *testing.T) {
	t.Helper()

	var fallback atomic.Bool
	hookCalled := make(chan struct{}, 1)
	debouncedForce := make(chan bool, 1)

	session := NewStreamSession(StreamSessionParams{
		TurnID:  "turn-5",
		AgentID: "agent-1",
		GetStreamTarget: func() StreamTarget {
			return StreamTarget{NetworkMessageID: networkid.MessageID("msg-5")}
		},
		ResolveTargetEventID: func(context.Context, StreamTarget) (id.EventID, error) {
			return id.EventID("$event-5"), nil
		},
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:example.com")
		},
		NextSeq:             func() int { return 1 },
		RuntimeFallbackFlag: &fallback,
		SendDebouncedEdit: func(_ context.Context, force bool) error {
			debouncedForce <- force
			return nil
		},
		SendHook: func(_ string, _ int, _ map[string]any, _ string) bool {
			hookCalled <- struct{}{}
			return true
		},
	})

	session.EmitPart(context.Background(), map[string]any{
		"type":       "tool-approval-response",
		"approvalId": "approval-1",
		"toolCallId": "tool-call-1",
		"approved":   true,
	})

	select {
	case <-hookCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected approval response to be streamed")
	}

	select {
	case force := <-debouncedForce:
		if !force {
			t.Fatal("expected approval resolution checkpoint edit to be forced")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected approval response to trigger a persisted checkpoint edit")
	}

	if fallback.Load() {
		t.Fatal("did not expect approval resolution checkpoint edit to switch stream transport into fallback mode")
	}
}
