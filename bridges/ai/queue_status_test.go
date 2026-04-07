package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
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
		PromptContext{},
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
		PromptContext{},
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

func TestDispatchOrQueueQueuesBehindExistingPendingWork(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		activeRooms:   map[id.RoomID]bool{},
		pendingQueues: map[id.RoomID]*pendingQueue{},
	}
	oc.pendingQueues[roomID] = &pendingQueue{
		items: []pendingQueueItem{
			{
				pending: pendingMessage{Type: pendingTypeText, MessageBody: "older"},
			},
		},
		cap:        10,
		dropPolicy: airuntime.QueueDropOld,
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
		PromptContext{},
	)

	if !isPending {
		t.Fatalf("expected pending=true when older queued work exists")
	}
	queue := oc.pendingQueues[roomID]
	if queue == nil {
		t.Fatalf("expected pending queue to exist")
	}
	if got := len(queue.items); got != 2 {
		t.Fatalf("expected queue length 2 after enqueue behind backlog, got %d", got)
	}
	if oc.activeRooms[roomID] {
		t.Fatalf("expected room to remain unacquired while backlog exists")
	}
}

func TestRemovePendingQueueBySourceEventClearsRemovedLastItem(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	first := pendingQueueItem{pending: pendingMessage{SourceEventID: id.EventID("$one")}}
	last := pendingQueueItem{pending: pendingMessage{SourceEventID: id.EventID("$two")}}
	oc := &AIClient{
		pendingQueues: map[id.RoomID]*pendingQueue{
			roomID: {
				items:    []pendingQueueItem{first, last},
				lastItem: &last,
			},
		},
	}

	removed := oc.removePendingQueueBySourceEvent(roomID, id.EventID("$two"))
	if len(removed) != 1 {
		t.Fatalf("expected one removed item, got %d", len(removed))
	}

	snapshot := oc.getQueueSnapshot(roomID)
	if snapshot == nil {
		t.Fatal("expected queue snapshot to remain")
	}
	if snapshot.lastItem == nil {
		t.Fatal("expected lastItem to be reassigned to the new tail")
	}
	if got := snapshot.lastItem.pending.sourceEventID(); got != id.EventID("$one") {
		t.Fatalf("expected lastItem to point at remaining item, got %q", got)
	}
}
