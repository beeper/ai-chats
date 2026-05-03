package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/ai-chats/pkg/runtime"
	"github.com/beeper/ai-chats/sdk"
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

func TestConsumeRoomRunAcceptedMessagesDrainsQueue(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	msg1 := &database.Message{ID: "msg1"}
	msg2 := &database.Message{ID: "msg2"}
	oc := &AIClient{
		activeRoomRuns: map[id.RoomID]*roomRunState{
			roomID: {
				acceptedUserMessages: []*database.Message{msg1, msg2},
			},
		},
	}

	got := oc.consumeRoomRunAcceptedMessages(roomID)
	if len(got) != 2 {
		t.Fatalf("expected 2 accepted messages, got %d", len(got))
	}
	if got[0] != msg1 || got[1] != msg2 {
		t.Fatalf("unexpected drained messages: %#v", got)
	}
	if again := oc.consumeRoomRunAcceptedMessages(roomID); len(again) != 0 {
		t.Fatalf("expected accepted message queue to be empty after drain, got %d", len(again))
	}
}

func TestConsumeRoomRunStatusEventsDrainsQueue(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	evt1 := &event.Event{ID: id.EventID("$one")}
	evt2 := &event.Event{ID: id.EventID("$two")}
	oc := &AIClient{
		activeRoomRuns: map[id.RoomID]*roomRunState{
			roomID: {
				statusEvents: []*event.Event{evt1, evt2},
			},
		},
	}

	got := oc.consumeRoomRunStatusEvents(roomID)
	if len(got) != 2 {
		t.Fatalf("expected 2 status events, got %d", len(got))
	}
	if got[0] != evt1 || got[1] != evt2 {
		t.Fatalf("unexpected drained status events: %#v", got)
	}
	if again := oc.consumeRoomRunStatusEvents(roomID); len(again) != 0 {
		t.Fatalf("expected status event queue to be empty after drain, got %d", len(again))
	}
}

func TestPromptCurrentUserVisibleTextPrefersCanonicalTurnData(t *testing.T) {
	got := promptCurrentUserVisibleText(PromptContext{
		CurrentTurnData: sdk.TurnData{
			Role: "user",
			Parts: []sdk.TurnPart{
				{Type: "text", Text: "first"},
				{Type: "tool", Text: "ignored"},
				{Type: "text", Text: "second"},
			},
		},
		Messages: []PromptMessage{{
			Role:   PromptRoleUser,
			Blocks: []PromptBlock{{Type: PromptBlockText, Text: "fallback"}},
		}},
	})
	if got != "first\nsecond" {
		t.Fatalf("unexpected canonical turn text: %q", got)
	}
}

func TestAcceptPendingMessagesPersistsMessagesAndSendsSuccessMSS(t *testing.T) {
	client := newDBBackedTestAIClient(t, ProviderMagicProxy)
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: networkid.PortalKey{
				ID:       "openai:room",
				Receiver: client.UserLogin.ID,
			},
		},
		Bridge: client.UserLogin.Bridge,
	}
	portal.MXID = roomID

	acceptedAt := time.UnixMilli(1776263886607)
	msg1 := &database.Message{
		ID:        networkid.MessageID("mx:$one"),
		MXID:      id.EventID("$one"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Timestamp: acceptedAt,
		Metadata:  &MessageMetadata{BaseMessageMetadata: sdk.BaseMessageMetadata{Role: "user", Body: "one"}},
	}
	setCanonicalTurnDataFromPromptMessages(msg1.Metadata.(*MessageMetadata), []PromptMessage{{
		Role:   PromptRoleUser,
		Blocks: []PromptBlock{{Type: PromptBlockText, Text: "one"}},
	}})
	msg2 := &database.Message{
		ID:        networkid.MessageID("mx:$two"),
		MXID:      id.EventID("$two"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Timestamp: acceptedAt.Add(time.Millisecond),
		Metadata:  &MessageMetadata{BaseMessageMetadata: sdk.BaseMessageMetadata{Role: "user", Body: "two"}},
	}
	setCanonicalTurnDataFromPromptMessages(msg2.Metadata.(*MessageMetadata), []PromptMessage{{
		Role:   PromptRoleUser,
		Blocks: []PromptBlock{{Type: PromptBlockText, Text: "two"}},
	}})
	state := &streamingState{roomID: roomID}
	client.activeRoomRuns = map[id.RoomID]*roomRunState{
		roomID: {
			state:                state,
			acceptedUserMessages: []*database.Message{msg1, msg2},
			statusEvents: []*event.Event{
				{ID: msg1.MXID, Type: event.EventMessage, RoomID: roomID},
				{ID: msg2.MXID, Type: event.EventMessage, RoomID: roomID},
			},
		},
	}

	client.acceptPendingMessages(context.Background(), portal, state)

	tmc, ok := client.UserLogin.Bridge.Matrix.(*testMatrixConnector)
	if !ok {
		t.Fatalf("expected test matrix connector")
	}
	if len(tmc.statuses) != 2 {
		t.Fatalf("expected 2 success statuses, got %d", len(tmc.statuses))
	}
	for i, status := range tmc.statuses {
		if status.Status != event.MessageStatusSuccess {
			t.Fatalf("status %d should be success, got %s", i, status.Status)
		}
	}
	if len(tmc.statusInfos) != 2 {
		t.Fatalf("expected 2 status infos, got %d", len(tmc.statusInfos))
	}
	if tmc.statusInfos[0].SourceEventID != msg1.MXID || tmc.statusInfos[1].SourceEventID != msg2.MXID {
		t.Fatalf("unexpected status event ids: %#v", tmc.statusInfos)
	}
	if got := client.consumeRoomRunAcceptedMessages(roomID); len(got) != 0 {
		t.Fatalf("expected accepted message queue to be empty after acceptance, got %d", len(got))
	}
	if got := client.consumeRoomRunStatusEvents(roomID); len(got) != 0 {
		t.Fatalf("expected status event queue to be empty after acceptance, got %d", len(got))
	}
	for _, msg := range []*database.Message{msg1, msg2} {
		saved, err := client.loadAIConversationMessage(context.Background(), portal, msg.ID, msg.MXID)
		if err != nil {
			t.Fatalf("get accepted message %s: %v", msg.MXID, err)
		}
		if saved == nil {
			t.Fatalf("expected accepted message for %s", msg.MXID)
		}
	}
}

func TestNotifyMatrixSendFailureSkipsMSSAfterAcceptance(t *testing.T) {
	client := newDBBackedTestAIClient(t, ProviderMagicProxy)
	roomID := id.RoomID("!room:example.com")
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: networkid.PortalKey{
				ID:       "openai:room",
				Receiver: client.UserLogin.ID,
			},
		},
		Bridge: client.UserLogin.Bridge,
	}
	portal.MXID = roomID
	state := &streamingState{roomID: roomID}
	state.markAccepted()
	client.activeRoomRuns = map[id.RoomID]*roomRunState{
		roomID: {
			state: state,
		},
	}

	client.notifyMatrixSendFailure(context.Background(), portal, &event.Event{
		ID:     id.EventID("$source"),
		Type:   event.EventMessage,
		RoomID: roomID,
	}, errors.New("boom"))

	tmc, ok := client.UserLogin.Bridge.Matrix.(*testMatrixConnector)
	if !ok {
		t.Fatalf("expected test matrix connector")
	}
	if len(tmc.statuses) != 0 {
		t.Fatalf("expected no failure MSS after acceptance, got %d", len(tmc.statuses))
	}
}

func TestDispatchOrQueueQueueRejectReturnsError(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		activeRoomRuns: map[id.RoomID]*roomRunState{roomID: {}},
		pendingQueues:  map[id.RoomID]*pendingQueue{},
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

	err := oc.dispatchOrQueueCore(
		context.Background(),
		evt,
		portal,
		nil,
		queueItem,
		airuntime.QueueSettings{Mode: airuntime.QueueModeCollect, Cap: 1, DropPolicy: airuntime.QueueDropNew},
		PromptContext{},
	)

	if err == nil {
		t.Fatalf("expected error when queue rejects the message")
	}
	if got := len(oc.pendingQueues[roomID].items); got != 1 {
		t.Fatalf("expected queue length to stay 1 after reject, got %d", got)
	}
}

func TestDispatchOrQueueQueueAcceptReturnsNil(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		activeRoomRuns: map[id.RoomID]*roomRunState{roomID: {}},
		pendingQueues:  map[id.RoomID]*pendingQueue{},
	}

	evt := &event.Event{ID: id.EventID("$new")}
	portal := &bridgev2.Portal{Portal: &database.Portal{}}
	portal.MXID = roomID
	queueItem := pendingQueueItem{
		pending:   pendingMessage{Type: pendingTypeText, MessageBody: "new"},
		messageID: string(evt.ID),
	}

	err := oc.dispatchOrQueueCore(
		context.Background(),
		evt,
		portal,
		nil,
		queueItem,
		airuntime.QueueSettings{Mode: airuntime.QueueModeCollect, Cap: 10, DropPolicy: airuntime.QueueDropOld},
		PromptContext{},
	)

	if err != nil {
		t.Fatalf("expected nil error when queue accepts the message, got %v", err)
	}
	queue := oc.pendingQueues[roomID]
	if queue == nil {
		t.Fatalf("expected pending queue to be created")
	}
	if got := len(queue.items); got != 1 {
		t.Fatalf("expected queue length 1 after accept, got %d", got)
	}
	if queue.items[0].pending.Portal != portal {
		t.Fatalf("expected queued item portal to be filled")
	}
	if queue.items[0].pending.Event != evt {
		t.Fatalf("expected queued item event to be filled")
	}
}

func TestDispatchOrQueueSteerBacklogDoesNotDoubleAccept(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	msg := &database.Message{ID: "msg"}
	oc := &AIClient{
		activeRoomRuns: map[id.RoomID]*roomRunState{roomID: {streaming: true}},
		pendingQueues:  map[id.RoomID]*pendingQueue{},
	}

	evt := &event.Event{ID: id.EventID("$new")}
	portal := &bridgev2.Portal{Portal: &database.Portal{}}
	portal.MXID = roomID
	queueItem := pendingQueueItem{
		pending:          pendingMessage{Type: pendingTypeText, MessageBody: "new"},
		acceptedMessages: []*database.Message{msg},
		messageID:        string(evt.ID),
	}

	err := oc.dispatchOrQueueCore(
		context.Background(),
		evt,
		portal,
		nil,
		queueItem,
		airuntime.QueueSettings{Mode: airuntime.QueueModeSteerBacklog, Cap: 10, DropPolicy: airuntime.QueueDropOld},
		PromptContext{},
	)

	if err != nil {
		t.Fatalf("expected nil error for steer+backlog, got %v", err)
	}
	run := oc.getRoomRun(roomID)
	if run == nil {
		t.Fatalf("expected active run")
	}
	if got := len(run.acceptedUserMessages); got != 1 {
		t.Fatalf("expected steering to register one accepted message, got %d", got)
	}
	queue := oc.pendingQueues[roomID]
	if queue == nil || len(queue.items) != 1 {
		t.Fatalf("expected one follow-up queued item, got %#v", queue)
	}
	if got := len(queue.items[0].acceptedMessages); got != 0 {
		t.Fatalf("expected queued follow-up not to duplicate accepted messages, got %d", got)
	}
	if got := len(queue.items[0].pending.StatusEvents); got != 0 {
		t.Fatalf("expected queued follow-up not to duplicate status events, got %d", got)
	}
}

func TestDispatchOrQueueQueuesBehindExistingPendingWork(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		activeRoomRuns: map[id.RoomID]*roomRunState{},
		pendingQueues:  map[id.RoomID]*pendingQueue{},
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

	err := oc.dispatchOrQueueCore(
		context.Background(),
		evt,
		portal,
		nil,
		queueItem,
		airuntime.QueueSettings{Mode: airuntime.QueueModeCollect, Cap: 10, DropPolicy: airuntime.QueueDropOld},
		PromptContext{},
	)

	if err != nil {
		t.Fatalf("expected nil error when older queued work exists, got %v", err)
	}
	queue := oc.pendingQueues[roomID]
	if queue == nil {
		t.Fatalf("expected pending queue to exist")
	}
	if got := len(queue.items); got != 2 {
		t.Fatalf("expected queue length 2 after enqueue behind backlog, got %d", got)
	}
	if oc.roomHasActiveRun(roomID) {
		t.Fatalf("expected room occupancy to remain clear while backlog exists")
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

func TestTakePendingQueueDispatchCandidateSummaryOnlyDoesNotPanic(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	last := pendingQueueItem{pending: pendingMessage{Type: pendingTypeText, MessageBody: "last"}}
	oc := &AIClient{
		pendingQueues: map[id.RoomID]*pendingQueue{
			roomID: {
				mode:         airuntime.QueueModeBacklog,
				dropPolicy:   airuntime.QueueDropSummarize,
				droppedCount: 1,
				summaryLines: []string{"dropped"},
				lastItem:     &last,
			},
		},
	}

	candidate := oc.takePendingQueueDispatchCandidate(roomID, false)
	if candidate == nil {
		t.Fatalf("expected synthetic candidate")
	}
	if !candidate.synthetic {
		t.Fatalf("expected synthetic candidate")
	}
	if len(candidate.items) != 1 || candidate.items[0].pending.MessageBody != "last" {
		t.Fatalf("unexpected synthetic item: %#v", candidate.items)
	}
}

func TestTakePendingQueueDispatchCandidateSummaryWithoutReplayItemDoesNotPanic(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		pendingQueues: map[id.RoomID]*pendingQueue{
			roomID: {
				mode:         airuntime.QueueModeBacklog,
				dropPolicy:   airuntime.QueueDropSummarize,
				droppedCount: 1,
				summaryLines: []string{"dropped"},
			},
		},
	}

	if candidate := oc.takePendingQueueDispatchCandidate(roomID, false); candidate != nil {
		t.Fatalf("expected no candidate without a replay item, got %#v", candidate)
	}
}
