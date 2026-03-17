package turns

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type testStreamTransport struct {
	descriptor    *event.BeeperStreamInfo
	startedRoom   id.RoomID
	startedEvent  id.EventID
	published     []map[string]any
	finishedEvent id.EventID
}

func (tst *testStreamTransport) BuildDescriptor(context.Context, *bridgev2.StreamDescriptorRequest) (*event.BeeperStreamInfo, error) {
	return tst.descriptor, nil
}

func (tst *testStreamTransport) Start(_ context.Context, req *bridgev2.StartStreamRequest) error {
	tst.startedRoom = req.RoomID
	tst.startedEvent = req.EventID
	return nil
}

func (tst *testStreamTransport) Publish(_ context.Context, req *bridgev2.PublishStreamRequest) error {
	tst.published = append(tst.published, req.Content)
	return nil
}

func (tst *testStreamTransport) Finish(_ context.Context, req *bridgev2.FinishStreamRequest) error {
	tst.finishedEvent = req.EventID
	return nil
}

func (tst *testStreamTransport) HandleIncomingEvent(context.Context, *event.Event) bool {
	return false
}

func TestStreamSessionDescriptorStartPublishFinish(t *testing.T) {
	transport := &testStreamTransport{
		descriptor: &event.BeeperStreamInfo{
			UserID:   id.UserID("@bot:example.com"),
			DeviceID: id.DeviceID("DEVICE"),
			Type:     "com.beeper.llm",
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
		GetStreamTransport: func(context.Context) (bridgev2.StreamTransport, bool) {
			return transport, true
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
	session.End(context.Background(), EndReasonFinish)

	if transport.startedRoom != id.RoomID("!room:example.com") || transport.startedEvent != id.EventID("$event-1") {
		t.Fatalf("unexpected start target: %s %s", transport.startedRoom, transport.startedEvent)
	}
	if len(transport.published) != 1 {
		t.Fatalf("expected one published update, got %d", len(transport.published))
	}
	if transport.finishedEvent != id.EventID("$event-1") {
		t.Fatalf("unexpected finish target: %s", transport.finishedEvent)
	}
}

func TestStreamSessionEmitPartUsesResolvedRelationTarget(t *testing.T) {
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
		GetStreamTransport: func(context.Context) (bridgev2.StreamTransport, bool) {
			return &testStreamTransport{descriptor: &event.BeeperStreamInfo{Type: "com.beeper.llm"}}, true
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
	relatesTo, ok := deltas[0]["m.relates_to"].(map[string]any)
	if !ok {
		t.Fatalf("expected m.relates_to in delta, got %#v", deltas[0])
	}
	if relatesTo["event_id"] != "$event-2" {
		t.Fatalf("unexpected relation target: %#v", relatesTo)
	}
}

func TestStreamSessionDoesNothingWithoutEditTarget(t *testing.T) {
	called := false
	session := NewStreamSession(StreamSessionParams{
		TurnID: "turn-3",
		GetStreamTarget: func() StreamTarget {
			return StreamTarget{}
		},
		GetStreamTransport: func(context.Context) (bridgev2.StreamTransport, bool) {
			return &testStreamTransport{descriptor: &event.BeeperStreamInfo{Type: "com.beeper.llm"}}, true
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
	transport := &testStreamTransport{descriptor: &event.BeeperStreamInfo{Type: "com.beeper.llm"}}
	var targetEventID id.EventID
	var seq int

	session := NewStreamSession(StreamSessionParams{
		TurnID: "turn-buffered",
		GetRoomID: func() id.RoomID {
			return id.RoomID("!room:example.com")
		},
		GetTargetEventID: func() id.EventID {
			return targetEventID
		},
		GetStreamTransport: func(context.Context) (bridgev2.StreamTransport, bool) {
			return transport, true
		},
		NextSeq: func() int {
			seq++
			return seq
		},
	})

	session.EmitPart(context.Background(), map[string]any{"type": "text-delta", "delta": "hello"})
	if len(transport.published) != 0 {
		t.Fatalf("expected part to stay buffered, got %#v", transport.published)
	}

	targetEventID = id.EventID("$event-buffered")
	if err := session.Start(context.Background(), targetEventID); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if transport.startedEvent != targetEventID {
		t.Fatalf("expected stream start target %s, got %s", targetEventID, transport.startedEvent)
	}
	if len(transport.published) != 1 {
		t.Fatalf("expected one buffered publish after target resolution, got %d", len(transport.published))
	}
}
