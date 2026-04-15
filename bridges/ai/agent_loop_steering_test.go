package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func getFollowUpMessagesForTest(oc *AIClient, roomID id.RoomID) []PromptMessage {
	if oc == nil || roomID == "" {
		return nil
	}
	snapshot := oc.getQueueSnapshot(roomID)
	if snapshot == nil {
		return nil
	}
	behavior := airuntime.ResolveQueueBehavior(snapshot.mode)
	if !behavior.Followup {
		return nil
	}
	candidate := oc.takePendingQueueDispatchCandidate(roomID, true)
	if candidate == nil || len(candidate.items) == 0 {
		return nil
	}
	_, prompt, ok := preparePendingQueueDispatchCandidate(candidate)
	if !ok {
		return nil
	}
	return buildSteeringPromptMessages([]string{prompt})
}

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
	got := buildSteeringPromptMessages([]string{"first", " ", "second"})
	if len(got) != 2 {
		t.Fatalf("expected 2 steering prompt messages, got %d", len(got))
	}
	if got[0].Role != PromptRoleUser || got[0].Text() != "first" {
		t.Fatalf("unexpected first steering prompt message: %#v", got[0])
	}
	if got[1].Role != PromptRoleUser || got[1].Text() != "second" {
		t.Fatalf("unexpected second steering prompt message: %#v", got[1])
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

	messages := getFollowUpMessagesForTest(oc, roomID)
	if len(messages) != 1 || messages[0].Role != PromptRoleUser || messages[0].Text() != "follow up" {
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

	messages := getFollowUpMessagesForTest(oc, roomID)
	if len(messages) != 1 || messages[0].Role != PromptRoleUser {
		t.Fatalf("expected one combined follow-up message, got %#v", messages)
	}
	if messages[0].Text() != "[Queued messages while agent was busy]\n\n---\nQueued #1\nfirst\n\n---\nQueued #2\nsecond" {
		t.Fatalf("unexpected combined follow-up prompt: %q", messages[0].Text())
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

	messages := getFollowUpMessagesForTest(oc, roomID)
	if len(messages) != 1 || messages[0].Role != PromptRoleUser {
		t.Fatalf("expected one combined follow-up message, got %#v", messages)
	}
	if messages[0].Text() != "[Queued messages while agent was busy]\n\n[Queue overflow] Dropped 2 messages due to cap.\nSummary:\n- older one\n- older two\n\n---\nQueued #1\nfirst\n\n---\nQueued #2\nsecond" {
		t.Fatalf("unexpected combined follow-up prompt with summary: %q", messages[0].Text())
	}

	if again := getFollowUpMessagesForTest(oc, roomID); len(again) != 0 {
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

	messages := getFollowUpMessagesForTest(oc, roomID)
	if len(messages) != 1 || messages[0].Role != PromptRoleUser {
		t.Fatalf("expected one synthetic follow-up message, got %#v", messages)
	}
	if messages[0].Text() != "[Queue overflow] Dropped 2 messages due to cap.\nSummary:\n- older one\n- older two" {
		t.Fatalf("unexpected synthetic follow-up prompt: %q", messages[0].Text())
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

	first := getFollowUpMessagesForTest(oc, roomID)
	if len(first) != 1 || first[0].Role != PromptRoleUser {
		t.Fatalf("expected one synthetic follow-up message, got %#v", first)
	}
	if first[0].Text() != "[Queue overflow] Dropped 2 messages due to cap.\nSummary:\n- older one\n- older two" {
		t.Fatalf("unexpected first synthetic follow-up prompt: %q", first[0].Text())
	}

	second := getFollowUpMessagesForTest(oc, roomID)
	if len(second) != 1 || second[0].Role != PromptRoleUser {
		t.Fatalf("expected queued latest message after summary, got %#v", second)
	}
	if second[0].Text() != "latest" {
		t.Fatalf("expected latest queued message after consuming summary, got %q", second[0].Text())
	}

	if third := getFollowUpMessagesForTest(oc, roomID); len(third) != 0 {
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

	messages := getFollowUpMessagesForTest(oc, roomID)
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

	messages := getFollowUpMessagesForTest(oc, roomID)
	if len(messages) != 0 {
		t.Fatalf("expected no follow-up messages for non-followup mode, got %#v", messages)
	}
	if snapshot := oc.getQueueSnapshot(roomID); snapshot == nil || len(snapshot.items) != 1 {
		t.Fatalf("expected queue to remain untouched, got %#v", snapshot)
	}
}

func TestBuildContinuationParams_UsesPendingSteeringPromptsBeforeDrainingQueue(t *testing.T) {
	roomID := id.RoomID("!room:example.com")
	newClient := func() *AIClient {
		return &AIClient{
			connector: &OpenAIConnector{},
			activeRoomRuns: map[id.RoomID]*roomRunState{
				roomID: {
					steerQueue: []pendingQueueItem{
						{pending: pendingMessage{Type: pendingTypeText, MessageBody: "queue steer"}},
					},
				},
			},
		}
	}

	t.Run("non-nil prompt", func(t *testing.T) {
		oc := newClient()
		state := &streamingState{roomID: roomID}
		state.addPendingSteeringPrompts([]string{"pending steer"})
		prompt := PromptContext{}

		params := oc.buildContinuationParams(context.Background(), &prompt, state, nil, nil, nil)
		if len(params.Input.OfInputItemList) == 0 {
			t.Fatal("expected continuation input to include stored steering prompt")
		}
		if pending := state.consumePendingSteeringPrompts(); len(pending) != 0 {
			t.Fatalf("expected pending steering prompts to be consumed, got %#v", pending)
		}
		if len(prompt.Messages) == 0 {
			t.Fatal("expected steering input to persist in canonical prompt even when history starts empty")
		}
		if snapshot := oc.getRoomRun(roomID); snapshot == nil || len(snapshot.steerQueue) != 1 {
			t.Fatalf("expected queued steering item to remain available, got %#v", snapshot)
		}
	})

	t.Run("nil prompt", func(t *testing.T) {
		oc := newClient()
		state := &streamingState{roomID: roomID}
		state.addPendingSteeringPrompts([]string{"pending steer"})

		params := oc.buildContinuationParams(context.Background(), nil, state, nil, nil, nil)
		if len(params.Input.OfInputItemList) == 0 {
			t.Fatal("expected continuation input to include stored steering prompt")
		}
		if pending := state.consumePendingSteeringPrompts(); len(pending) != 0 {
			t.Fatalf("expected pending steering prompts to be consumed, got %#v", pending)
		}
		if snapshot := oc.getRoomRun(roomID); snapshot == nil || len(snapshot.steerQueue) != 1 {
			t.Fatalf("expected queued steering item to remain available, got %#v", snapshot)
		}
	})
}
