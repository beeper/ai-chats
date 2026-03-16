package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func TestGetSteeringMessages_FiltersAndDrainsQueue(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		connector: &OpenAIConnector{},
		activeRoomRuns: map[id.RoomID]*roomRunState{
			roomID: {
				steerQueue: []pendingQueueItem{
					{
						pending: pendingMessage{Type: pendingTypeText, MessageBody: "fallback"},
						prompt:  "  explicit steer  ",
					},
					{
						pending: pendingMessage{Type: pendingTypeText, MessageBody: "body only"},
					},
					{
						pending: pendingMessage{Type: pendingTypeImage, MessageBody: "ignored"},
						prompt:  "ignored",
					},
					{
						pending: pendingMessage{Type: pendingTypeText, MessageBody: "   "},
						prompt:  "   ",
					},
				},
			},
		},
	}

	got := oc.getSteeringMessages(roomID)
	if len(got) != 2 {
		t.Fatalf("expected 2 steering messages, got %d: %#v", len(got), got)
	}
	if got[0] != "explicit steer" {
		t.Fatalf("expected first steering prompt to prefer explicit prompt, got %q", got[0])
	}
	if got[1] != "body only" {
		t.Fatalf("expected second steering prompt to fallback to message body, got %q", got[1])
	}

	if again := oc.getSteeringMessages(roomID); len(again) != 0 {
		t.Fatalf("expected steering queue to be drained, got %#v", again)
	}
}

func TestBuildSteeringUserMessages(t *testing.T) {
	got := buildSteeringUserMessages([]string{"first", " ", "second"})
	if len(got) != 2 {
		t.Fatalf("expected 2 steering user messages, got %d", len(got))
	}
	if got[0].OfUser == nil || got[0].OfUser.Content.OfString.Value != "first" {
		t.Fatalf("unexpected first steering user message: %#v", got[0])
	}
	if got[1].OfUser == nil || got[1].OfUser.Content.OfString.Value != "second" {
		t.Fatalf("unexpected second steering user message: %#v", got[1])
	}
}

func TestGetFollowUpMessages_ConsumesSingleQueuedTextMessage(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		pendingQueues: map[id.RoomID]*pendingQueue{
			roomID: {
				mode: airuntime.QueueModeFollowup,
				items: []pendingQueueItem{
					{pending: pendingMessage{Type: pendingTypeText, MessageBody: "follow up"}},
				},
			},
		},
	}

	messages := oc.getFollowUpMessages(roomID)
	if len(messages) != 1 || messages[0].OfUser == nil || messages[0].OfUser.Content.OfString.Value != "follow up" {
		t.Fatalf("unexpected follow-up messages: %#v", messages)
	}
	if snapshot := oc.getQueueSnapshot(roomID); snapshot != nil {
		t.Fatalf("expected queue to be drained, got %#v", snapshot.items)
	}
}

func TestGetFollowUpMessages_CollectsQueuedTextMessages(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		pendingQueues: map[id.RoomID]*pendingQueue{
			roomID: {
				mode: airuntime.QueueModeCollect,
				items: []pendingQueueItem{
					{pending: pendingMessage{Type: pendingTypeText, MessageBody: "first"}},
					{pending: pendingMessage{Type: pendingTypeText, MessageBody: "second"}},
				},
			},
		},
	}

	messages := oc.getFollowUpMessages(roomID)
	if len(messages) != 1 || messages[0].OfUser == nil {
		t.Fatalf("expected one combined follow-up message, got %#v", messages)
	}
	if messages[0].OfUser.Content.OfString.Value != "[Queued messages while agent was busy]\n\n---\nQueued #1\nfirst\n\n---\nQueued #2\nsecond" {
		t.Fatalf("unexpected combined follow-up prompt: %q", messages[0].OfUser.Content.OfString.Value)
	}
}

func TestGetFollowUpMessages_CollectSummaryIsConsumed(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		pendingQueues: map[id.RoomID]*pendingQueue{
			roomID: {
				mode:         airuntime.QueueModeCollect,
				dropPolicy:   airuntime.QueueDropSummarize,
				droppedCount: 2,
				summaryLines: []string{"older one", "older two"},
				items: []pendingQueueItem{
					{pending: pendingMessage{Type: pendingTypeText, MessageBody: "first"}},
					{pending: pendingMessage{Type: pendingTypeText, MessageBody: "second"}},
				},
			},
		},
	}

	messages := oc.getFollowUpMessages(roomID)
	if len(messages) != 1 || messages[0].OfUser == nil {
		t.Fatalf("expected one combined follow-up message, got %#v", messages)
	}
	if messages[0].OfUser.Content.OfString.Value != "[Queued messages while agent was busy]\n\n[Queue overflow] Dropped 2 messages due to cap.\nSummary:\n- older one\n- older two\n\n---\nQueued #1\nfirst\n\n---\nQueued #2\nsecond" {
		t.Fatalf("unexpected combined follow-up prompt with summary: %q", messages[0].OfUser.Content.OfString.Value)
	}

	if again := oc.getFollowUpMessages(roomID); len(again) != 0 {
		t.Fatalf("expected collect summary to be consumed, got %#v", again)
	}
	if snapshot := oc.getQueueSnapshot(roomID); snapshot != nil {
		t.Fatalf("expected queue to be fully drained after collect dispatch, got %#v", snapshot)
	}
}

func TestGetFollowUpMessages_UsesSyntheticSummaryPrompt(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		pendingQueues: map[id.RoomID]*pendingQueue{
			roomID: {
				mode:         airuntime.QueueModeFollowup,
				dropPolicy:   airuntime.QueueDropSummarize,
				droppedCount: 2,
				summaryLines: []string{"older one", "older two"},
				items: []pendingQueueItem{
					{pending: pendingMessage{Type: pendingTypeText, MessageBody: "latest"}},
				},
			},
		},
	}

	messages := oc.getFollowUpMessages(roomID)
	if len(messages) != 1 || messages[0].OfUser == nil {
		t.Fatalf("expected one synthetic follow-up message, got %#v", messages)
	}
	if messages[0].OfUser.Content.OfString.Value != "[Queue overflow] Dropped 2 messages due to cap.\nSummary:\n- older one\n- older two" {
		t.Fatalf("unexpected synthetic follow-up prompt: %q", messages[0].OfUser.Content.OfString.Value)
	}
}

func TestGetFollowUpMessages_SyntheticSummaryIsConsumedBeforeLatestMessage(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		pendingQueues: map[id.RoomID]*pendingQueue{
			roomID: {
				mode:         airuntime.QueueModeFollowup,
				dropPolicy:   airuntime.QueueDropSummarize,
				droppedCount: 2,
				summaryLines: []string{"older one", "older two"},
				items: []pendingQueueItem{
					{pending: pendingMessage{Type: pendingTypeText, MessageBody: "latest"}},
				},
			},
		},
	}

	first := oc.getFollowUpMessages(roomID)
	if len(first) != 1 || first[0].OfUser == nil {
		t.Fatalf("expected one synthetic follow-up message, got %#v", first)
	}
	if first[0].OfUser.Content.OfString.Value != "[Queue overflow] Dropped 2 messages due to cap.\nSummary:\n- older one\n- older two" {
		t.Fatalf("unexpected first synthetic follow-up prompt: %q", first[0].OfUser.Content.OfString.Value)
	}

	second := oc.getFollowUpMessages(roomID)
	if len(second) != 1 || second[0].OfUser == nil {
		t.Fatalf("expected queued latest message after summary, got %#v", second)
	}
	if second[0].OfUser.Content.OfString.Value != "latest" {
		t.Fatalf("expected latest queued message after consuming summary, got %q", second[0].OfUser.Content.OfString.Value)
	}

	if third := oc.getFollowUpMessages(roomID); len(third) != 0 {
		t.Fatalf("expected queue to be drained after latest message, got %#v", third)
	}
}

func TestGetFollowUpMessages_LeavesNonTextQueueItemsForBacklogProcessing(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		pendingQueues: map[id.RoomID]*pendingQueue{
			roomID: {
				mode: airuntime.QueueModeFollowup,
				items: []pendingQueueItem{
					{pending: pendingMessage{Type: pendingTypeImage, MessageBody: "image"}},
				},
			},
		},
	}

	messages := oc.getFollowUpMessages(roomID)
	if len(messages) != 0 {
		t.Fatalf("expected non-text follow-up to stay queued, got %#v", messages)
	}
	if snapshot := oc.getQueueSnapshot(roomID); snapshot == nil || len(snapshot.items) != 1 {
		t.Fatalf("expected non-text queue item to remain queued, got %#v", snapshot)
	}
}

func TestGetFollowUpMessages_LeavesNonFollowupQueueUntouched(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		pendingQueues: map[id.RoomID]*pendingQueue{
			roomID: {
				mode: airuntime.QueueModeSteer,
				items: []pendingQueueItem{
					{pending: pendingMessage{Type: pendingTypeText, MessageBody: "stay queued"}},
				},
			},
		},
	}

	messages := oc.getFollowUpMessages(roomID)
	if len(messages) != 0 {
		t.Fatalf("expected no follow-up messages for non-followup mode, got %#v", messages)
	}
	if snapshot := oc.getQueueSnapshot(roomID); snapshot == nil || len(snapshot.items) != 1 {
		t.Fatalf("expected queue to remain untouched, got %#v", snapshot)
	}
}

func TestBuildContinuationParams_UsesPendingSteeringPromptsBeforeDrainingQueue(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	oc := &AIClient{
		connector: &OpenAIConnector{},
		activeRoomRuns: map[id.RoomID]*roomRunState{
			roomID: {
				steerQueue: []pendingQueueItem{
					{pending: pendingMessage{Type: pendingTypeText, MessageBody: "queue steer"}},
				},
			},
		},
	}
	state := &streamingState{roomID: roomID}
	state.addPendingSteeringPrompts([]string{"pending steer"})

	params := oc.buildContinuationParams(context.Background(), state, nil, nil, nil)
	if len(params.Input.OfInputItemList) == 0 {
		t.Fatal("expected continuation input to include stored steering prompt")
	}
	if pending := state.consumePendingSteeringPrompts(); len(pending) != 0 {
		t.Fatalf("expected pending steering prompts to be consumed, got %#v", pending)
	}
	if len(state.baseInput) == 0 {
		t.Fatal("expected steering input to persist in base input even when history starts empty")
	}
	if snapshot := oc.getRoomRun(roomID); snapshot == nil || len(snapshot.steerQueue) != 1 {
		t.Fatalf("expected queued steering item to remain available, got %#v", snapshot)
	}
}
