package connector

import (
	"context"
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/ai-chats/pkg/runtime"
)

func TestPendingQueueCollectsBusyRoomMessagesIntoOneTurn(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		activeRoomRuns: map[id.RoomID]*roomRunState{roomID: {}},
		pendingQueues:  map[id.RoomID]*pendingQueue{},
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{}}
	portal.MXID = roomID
	settings := airuntime.QueueSettings{Mode: airuntime.QueueModeCollect, Cap: 10, DropPolicy: airuntime.QueueDropOld}

	for i, body := range []string{"one", "two", "three", "four", "five"} {
		evt := &event.Event{ID: id.EventID("$" + body)}
		item := pendingQueueItem{
			pending: pendingMessage{
				Type:        pendingTypeText,
				MessageBody: body,
			},
			acceptedMessages: []*database.Message{{ID: networkid.MessageID(body)}},
			messageID:        string(evt.ID),
		}
		err := oc.dispatchOrQueueCore(context.Background(), evt, portal, nil, item, settings, PromptContext{})
		if err != nil {
			t.Fatalf("queue message %d: %v", i, err)
		}
	}

	candidate := oc.takePendingQueueDispatchCandidate(roomID, false)
	item, prompt, ok := preparePendingQueueDispatchCandidate(candidate)
	if !ok {
		t.Fatalf("expected collected dispatch candidate")
	}
	for _, body := range []string{"one", "two", "three", "four", "five"} {
		if !strings.Contains(prompt, body) {
			t.Fatalf("expected prompt to contain %q, got %q", body, prompt)
		}
	}
	if got := len(item.acceptedMessages); got != 5 {
		t.Fatalf("expected five accepted messages in one follow-up turn, got %d", got)
	}
	if remaining := oc.getQueueSnapshot(roomID); remaining != nil && len(remaining.items) != 0 {
		t.Fatalf("expected queue to be drained into one candidate, got %#v", remaining.items)
	}
}
