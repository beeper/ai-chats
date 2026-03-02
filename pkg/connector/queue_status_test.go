package connector

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

func TestQueueStatusEventsDeduplicates(t *testing.T) {
	primary := &event.Event{ID: id.EventID("$primary")}
	extras := []*event.Event{
		{ID: id.EventID("$extra1")},
		{ID: id.EventID("$primary")},
		{ID: id.EventID("$extra2")},
		nil,
		{ID: ""},
	}

	got := queueStatusEvents(primary, extras)
	if len(got) != 3 {
		t.Fatalf("expected 3 status events, got %d", len(got))
	}
	if got[0].ID != primary.ID {
		t.Fatalf("expected primary first, got %s", got[0].ID)
	}
	if got[1].ID != id.EventID("$extra1") {
		t.Fatalf("expected extra1 second, got %s", got[1].ID)
	}
	if got[2].ID != id.EventID("$extra2") {
		t.Fatalf("expected extra2 third, got %s", got[2].ID)
	}
}

func TestMarkMessageSendSuccessSkippedWhenQueueAccepted(t *testing.T) {
	oc := &AIClient{}
	state := &streamingState{}
	evt := &event.Event{ID: id.EventID("$event")}

	oc.markMessageSendSuccess(context.WithValue(context.Background(), queueAcceptedStatusKey{}, true), nil, evt, state)

	if state.statusSent {
		t.Fatalf("expected statusSent=false when queue accepted marker is set")
	}
	if len(state.statusSentIDs) != 0 {
		t.Fatalf("expected no status IDs to be tracked, got %d", len(state.statusSentIDs))
	}

	oc.markMessageSendSuccess(context.Background(), nil, evt, state)
	if !state.statusSent {
		t.Fatalf("expected statusSent=true without queue accepted marker")
	}
	if len(state.statusSentIDs) != 1 {
		t.Fatalf("expected 1 tracked status ID, got %d", len(state.statusSentIDs))
	}
}

func TestDispatchOrQueueQueueRejectReturnsNotPending(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		activeRooms:   map[id.RoomID]bool{roomID: true},
		pendingQueues: map[id.RoomID]*pendingQueue{},
	}
	oc.pendingQueues[roomID] = &pendingQueue{
		items: []pendingQueueItem{
			{
				pending: pendingMessage{Type: pendingTypeText, MessageBody: "existing"},
			},
		},
		cap:        1,
		dropPolicy: airuntime.QueueDropNew,
	}

	evt := &event.Event{ID: id.EventID("$new")}
	portal := &bridgev2.Portal{Portal: &database.Portal{}}
	portal.MXID = roomID
	queueItem := pendingQueueItem{
		pending:   pendingMessage{Type: pendingTypeText, MessageBody: "new"},
		messageID: string(evt.ID),
	}

	_, isPending := oc.dispatchOrQueue(
		context.Background(),
		evt,
		portal,
		nil,
		nil,
		queueItem,
		airuntime.QueueSettings{Mode: airuntime.QueueModeCollect, Cap: 1, DropPolicy: airuntime.QueueDropNew},
		nil,
	)

	if isPending {
		t.Fatalf("expected pending=false when queue rejects the message")
	}
	if got := len(oc.pendingQueues[roomID].items); got != 1 {
		t.Fatalf("expected queue length to stay 1 after reject, got %d", got)
	}
}

func TestDispatchOrQueueQueueAcceptReturnsPending(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		activeRooms:   map[id.RoomID]bool{roomID: true},
		pendingQueues: map[id.RoomID]*pendingQueue{},
	}

	evt := &event.Event{ID: id.EventID("$new")}
	portal := &bridgev2.Portal{Portal: &database.Portal{}}
	portal.MXID = roomID
	queueItem := pendingQueueItem{
		pending:   pendingMessage{Type: pendingTypeText, MessageBody: "new"},
		messageID: string(evt.ID),
	}

	_, isPending := oc.dispatchOrQueue(
		context.Background(),
		evt,
		portal,
		nil,
		nil,
		queueItem,
		airuntime.QueueSettings{Mode: airuntime.QueueModeCollect, Cap: 10, DropPolicy: airuntime.QueueDropOld},
		nil,
	)

	if !isPending {
		t.Fatalf("expected pending=true when queue accepts the message")
	}
	queue := oc.pendingQueues[roomID]
	if queue == nil {
		t.Fatalf("expected pending queue to be created")
	}
	if got := len(queue.items); got != 1 {
		t.Fatalf("expected queue length 1 after accept, got %d", got)
	}
}
