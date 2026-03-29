package turns

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type testStreamPublisher struct {
	descriptor    *event.BeeperStreamInfo
	startedRoom   id.RoomID
	startedEvent  id.EventID
	registerCalls int
	registerErr   error
	published     []map[string]any
	finishedEvent id.EventID
}

func (tst *testStreamPublisher) NewDescriptor(_ context.Context, _ id.RoomID, streamType string) (*event.BeeperStreamInfo, error) {
	if tst.descriptor == nil {
		return &event.BeeperStreamInfo{
			UserID: id.UserID("@bot:example.com"),
			Type:   streamType,
		}, nil
	}
	descriptor := *tst.descriptor
	if descriptor.Type == "" {
		descriptor.Type = streamType
	}
	return &descriptor, nil
}

func (tst *testStreamPublisher) Register(_ context.Context, roomID id.RoomID, eventID id.EventID, _ *event.BeeperStreamInfo) error {
	tst.registerCalls++
	tst.startedRoom = roomID
	tst.startedEvent = eventID
	return tst.registerErr
}

func (tst *testStreamPublisher) Publish(_ context.Context, _ id.RoomID, _ id.EventID, content map[string]any) error {
	tst.published = append(tst.published, content)
	return nil
}

func (tst *testStreamPublisher) Unregister(_ id.RoomID, eventID id.EventID) {
	tst.finishedEvent = eventID
}

func pendingPartCount(session *StreamSession) int {
	if session == nil {
		return 0
	}
	session.streamMu.Lock()
	defer session.streamMu.Unlock()
	return len(session.pendingParts)
}

func TestStreamSessionDescriptorStartPublishFinish(t *testing.T) {
	publisher := &testStreamPublisher{
		descriptor: &event.BeeperStreamInfo{
			UserID: id.UserID("@bot:example.com"),
			Type:   "com.beeper.llm",
		},
	}

	session := NewStreamSession(StreamSessionParams{
		TurnID: "turn-1",
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:example.com")
		},
		GetTargetEventID: func() id.EventID {
			return id.EventID("$event-1")
		},
		GetStreamPublisher: func(context.Context) (bridgev2.BeeperStreamPublisher, bool) {
			return publisher, true
		},
		NextSeq: func() int { return 1 },
	})

	descriptor, err := session.Descriptor(context.Background())
	if err != nil {
		t.Fatalf("Descriptor() error = %v", err)
	}
	if descriptor == nil || descriptor.Type != "com.beeper.llm" {
		t.Fatalf("unexpected descriptor: %#v", descriptor)
	}

	if err = session.Start(context.Background(), id.EventID("$event-1")); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	session.EmitPart(context.Background(), map[string]any{"type": "text-delta", "delta": "hello"})
	if pendingPartCount(session) != 0 {
		t.Fatalf("expected no buffered parts after publish, got %d", pendingPartCount(session))
	}
	session.End(context.Background(), EndReasonFinish)
	if publisher.startedRoom != id.RoomID("!room:example.com") || publisher.startedEvent != id.EventID("$event-1") {
		t.Fatalf("unexpected start target: %s %s", publisher.startedRoom, publisher.startedEvent)
	}
	if len(publisher.published) != 1 {
		t.Fatalf("expected one published update, got %d", len(publisher.published))
	}
	if publisher.finishedEvent != id.EventID("$event-1") {
		t.Fatalf("unexpected finish target: %s", publisher.finishedEvent)
	}
	if !session.IsClosed() {
		t.Fatal("expected session to be closed after End")
	}
}

func TestStreamSessionEmitPartUsesResolvedRelationTarget(t *testing.T) {
	publisher := &testStreamPublisher{
		descriptor: &event.BeeperStreamInfo{Type: "com.beeper.llm"},
	}
	var gotContent map[string]any
	session := NewStreamSession(StreamSessionParams{
		TurnID:  "turn-2",
		AgentID: "agent-1",
		GetStreamTarget: func() StreamTarget {
			return StreamTarget{NetworkMessageID: "msg-2"}
		},
		ResolveTargetEventID: func(context.Context, StreamTarget) (id.EventID, error) {
			return id.EventID("$event-2"), nil
		},
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:example.com")
		},
		GetStreamType: func() string {
			return "com.beeper.llm"
		},
		GetStreamPublisher: func(context.Context) (bridgev2.BeeperStreamPublisher, bool) {
			return publisher, true
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
	deltas, ok := gotContent["com.beeper.llm.deltas"].([]map[string]any)
	if !ok {
		t.Fatalf("expected com.beeper.llm.deltas, got %#v", gotContent)
	}
	if len(deltas) != 1 {
		t.Fatalf("expected one delta, got %#v", deltas)
	}
	relatesTo, ok := deltas[0]["m.relates_to"].(*event.RelatesTo)
	if !ok {
		t.Fatalf("expected m.relates_to in delta, got %#v", deltas[0])
	}
	if relatesTo.EventID != id.EventID("$event-2") {
		t.Fatalf("unexpected relation target: %#v", relatesTo)
	}
}

func TestStreamSessionUsesConfiguredStreamTypeDeltaKey(t *testing.T) {
	publisher := &testStreamPublisher{
		descriptor: &event.BeeperStreamInfo{Type: "com.beeper.live_location"},
	}
	var gotContent map[string]any
	session := NewStreamSession(StreamSessionParams{
		TurnID: "turn-custom",
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:example.com")
		},
		GetTargetEventID: func() id.EventID {
			return id.EventID("$event-custom")
		},
		GetStreamType: func() string {
			return "com.beeper.live_location"
		},
		GetStreamPublisher: func(context.Context) (bridgev2.BeeperStreamPublisher, bool) {
			return publisher, true
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
	if _, ok := gotContent["com.beeper.live_location.deltas"]; !ok {
		t.Fatalf("expected custom stream delta key, got %#v", gotContent)
	}
}

func TestStreamSessionDoesNothingWithoutEditTarget(t *testing.T) {
	publisher := &testStreamPublisher{
		descriptor: &event.BeeperStreamInfo{Type: "com.beeper.llm"},
	}
	called := false
	session := NewStreamSession(StreamSessionParams{
		TurnID: "turn-3",
		GetStreamTarget: func() StreamTarget {
			return StreamTarget{}
		},
		GetStreamPublisher: func(context.Context) (bridgev2.BeeperStreamPublisher, bool) {
			return publisher, true
		},
		NextSeq: func() int { return 1 },
		SendHook: func(_ string, _ int, _ map[string]any, _ string) bool {
			called = true
			return true
		},
	})

	session.EmitPart(context.Background(), map[string]any{"type": "finish"})
	if called {
		t.Fatal("did not expect stream send without an edit target")
	}
}

func TestStreamSessionBuffersUntilTargetEventIDExists(t *testing.T) {
	publisher := &testStreamPublisher{
		descriptor: &event.BeeperStreamInfo{Type: "com.beeper.llm"},
	}
	var targetEventID id.EventID
	var seq int
	sendCount := 0

	session := NewStreamSession(StreamSessionParams{
		TurnID: "turn-buffered",
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:example.com")
		},
		GetTargetEventID: func() id.EventID {
			return targetEventID
		},
		GetStreamPublisher: func(context.Context) (bridgev2.BeeperStreamPublisher, bool) {
			return publisher, true
		},
		NextSeq: func() int {
			seq++
			return seq
		},
		SendHook: func(_ string, _ int, _ map[string]any, _ string) bool {
			sendCount++
			return true
		},
	})

	session.EmitPart(context.Background(), map[string]any{"type": "text-delta", "delta": "hello"})
	if sendCount != 0 {
		t.Fatalf("expected part to stay buffered until target is resolved, got %d sends", sendCount)
	}
	if pendingPartCount(session) != 1 {
		t.Fatalf("expected one pending part before stream start, got %d", pendingPartCount(session))
	}

	targetEventID = id.EventID("$event-buffered")
	if err := session.Start(context.Background(), targetEventID); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if publisher.startedEvent != targetEventID {
		t.Fatalf("expected stream start target %s, got %s", targetEventID, publisher.startedEvent)
	}
	if sendCount != 1 {
		t.Fatalf("expected one buffered publish after target resolution, got %d", sendCount)
	}
	if pendingPartCount(session) != 0 {
		t.Fatalf("expected no pending parts after stream start, got %d", pendingPartCount(session))
	}
}

func TestStreamSessionDescriptorRetriesAfterPublisherBecomesAvailable(t *testing.T) {
	publisher := &testStreamPublisher{
		descriptor: &event.BeeperStreamInfo{Type: "com.beeper.llm"},
	}
	var current bridgev2.BeeperStreamPublisher

	session := NewStreamSession(StreamSessionParams{
		TurnID: "turn-retry",
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:example.com")
		},
		GetStreamPublisher: func(context.Context) (bridgev2.BeeperStreamPublisher, bool) {
			if current == nil {
				return nil, false
			}
			return current, true
		},
	})

	if _, err := session.Descriptor(context.Background()); err != ErrNoPublisher {
		t.Fatalf("expected ErrNoPublisher before publisher is ready, got %v", err)
	}

	current = publisher

	descriptor, err := session.Descriptor(context.Background())
	if err != nil {
		t.Fatalf("Descriptor() after publisher ready error = %v", err)
	}
	if descriptor == nil || descriptor.Type != "com.beeper.llm" {
		t.Fatalf("unexpected descriptor after retry: %#v", descriptor)
	}
}

func TestStreamSessionHookOnlyFlushesWithoutPublisher(t *testing.T) {
	var sent map[string]any
	session := NewStreamSession(StreamSessionParams{
		TurnID: "turn-hook-only",
		GetTargetEventID: func() id.EventID {
			return id.EventID("$event-hook-only")
		},
		NextSeq: func() int { return 1 },
		SendHook: func(_ string, _ int, content map[string]any, _ string) bool {
			sent = content
			return true
		},
	})

	session.EmitPart(context.Background(), map[string]any{"type": "text-delta", "delta": "hello"})

	if sent == nil {
		t.Fatal("expected hook-only stream session to flush content")
	}
	if pendingPartCount(session) != 0 {
		t.Fatalf("expected no pending parts after hook-only flush, got %d", pendingPartCount(session))
	}
	if !session.streamStarted {
		t.Fatal("expected session to mark stream as started in hook-only mode")
	}
}

func TestStreamSessionRetriesRegisterWhenRoomBecomesAvailable(t *testing.T) {
	publisher := &testStreamPublisher{
		descriptor: &event.BeeperStreamInfo{Type: "com.beeper.llm"},
	}
	roomID := id.RoomID("")
	sendCount := 0
	session := NewStreamSession(StreamSessionParams{
		TurnID: "turn-register-retry",
		GetRoomID: func() id.RoomID {
			return roomID
		},
		GetTargetEventID: func() id.EventID {
			return id.EventID("$event-register-retry")
		},
		GetStreamPublisher: func(context.Context) (bridgev2.BeeperStreamPublisher, bool) {
			return publisher, true
		},
		NextSeq: func() int { return 1 },
		SendHook: func(_ string, _ int, _ map[string]any, _ string) bool {
			sendCount++
			return true
		},
	})

	session.EmitPart(context.Background(), map[string]any{"type": "text-delta", "delta": "hello"})
	if session.streamStarted {
		t.Fatal("expected session to remain unstarted until publisher registration succeeds")
	}
	if sendCount != 1 {
		t.Fatalf("expected hook to flush while waiting for room id, got %d sends", sendCount)
	}
	if publisher.registerCalls != 0 {
		t.Fatalf("expected no register attempts before room is available, got %d", publisher.registerCalls)
	}

	roomID = id.RoomID("!room:example.com")
	if err := session.Start(context.Background(), id.EventID("$event-register-retry")); err != nil {
		t.Fatalf("Start() after room became available error = %v", err)
	}
	if !session.streamStarted {
		t.Fatal("expected session to start after publisher registration succeeds")
	}
	if publisher.registerCalls != 1 {
		t.Fatalf("expected one register attempt after room became available, got %d", publisher.registerCalls)
	}
}

func TestStreamSessionCurrentTargetFallsBackToStartedTarget(t *testing.T) {
	publisher := &testStreamPublisher{
		descriptor: &event.BeeperStreamInfo{Type: "com.beeper.llm"},
	}
	sendCount := 0
	session := NewStreamSession(StreamSessionParams{
		TurnID: "turn-target-fallback",
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:example.com")
		},
		GetTargetEventID: func() id.EventID {
			return ""
		},
		GetStreamPublisher: func(context.Context) (bridgev2.BeeperStreamPublisher, bool) {
			return publisher, true
		},
		NextSeq: func() int { return 1 },
		SendHook: func(_ string, _ int, _ map[string]any, _ string) bool {
			sendCount++
			return true
		},
	})

	if err := session.Start(context.Background(), id.EventID("$event-target-fallback")); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	session.EmitPart(context.Background(), map[string]any{"type": "text-delta", "delta": "hello"})
	if sendCount != 1 {
		t.Fatalf("expected emit to reuse stored target event id, got %d sends", sendCount)
	}
	if pendingPartCount(session) != 0 {
		t.Fatalf("expected no buffered parts after fallback publish, got %d", pendingPartCount(session))
	}
}
