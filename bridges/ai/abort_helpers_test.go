package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	bridgesdk "github.com/beeper/agentremote/sdk"
)

func TestResolveUserStopPlanRoomWideWithoutReply(t *testing.T) {
	oc := &AIClient{}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:test"}}
	req := userStopRequest{Portal: portal, RequestedVia: "command"}

	plan := oc.resolveUserStopPlan(req)
	if plan.Kind != stopPlanKindRoomWide {
		t.Fatalf("expected room-wide stop, got %#v", plan)
	}
	if plan.TargetKind != "all" || plan.Scope != "room" {
		t.Fatalf("unexpected room-wide stop plan: %#v", plan)
	}
}

func TestResolveUserStopPlanMatchesActiveReplyTargets(t *testing.T) {
	roomID := id.RoomID("!room:test")
	oc := &AIClient{
		activeRoomRuns: map[id.RoomID]*roomRunState{
			roomID: {
				sourceEvent:  id.EventID("$user"),
				initialEvent: id.EventID("$assistant"),
			},
		},
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}

	placeholderPlan := oc.resolveUserStopPlan(userStopRequest{
		Portal:  portal,
		ReplyTo: id.EventID("$assistant"),
	})
	if placeholderPlan.Kind != stopPlanKindActive || placeholderPlan.TargetKind != "placeholder_event" {
		t.Fatalf("expected placeholder-targeted active stop, got %#v", placeholderPlan)
	}

	sourcePlan := oc.resolveUserStopPlan(userStopRequest{
		Portal:  portal,
		ReplyTo: id.EventID("$user"),
	})
	if sourcePlan.Kind != stopPlanKindActive || sourcePlan.TargetKind != "source_event" {
		t.Fatalf("expected source-targeted active stop, got %#v", sourcePlan)
	}
}

func TestResolveUserStopPlanSpeculativelyReturnsQueued(t *testing.T) {
	oc := &AIClient{}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:test"}}

	plan := oc.resolveUserStopPlan(userStopRequest{
		Portal:  portal,
		ReplyTo: id.EventID("$unknown"),
	})
	if plan.Kind != stopPlanKindQueued || plan.TargetKind != "source_event" {
		t.Fatalf("expected speculative queued stop plan, got %#v", plan)
	}
}

func TestExecuteUserStopPlanFallsBackToNoMatch(t *testing.T) {
	oc := &AIClient{}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:test"}}

	result := oc.executeUserStopPlan(context.Background(), userStopRequest{
		Portal: portal,
	}, userStopPlan{
		Kind:          stopPlanKindQueued,
		Scope:         "turn",
		TargetKind:    "source_event",
		TargetEventID: id.EventID("$nonexistent"),
	})
	if result.Plan.Kind != stopPlanKindNoMatch {
		t.Fatalf("expected no-match fallback, got %#v", result.Plan)
	}
	if result.QueuedStopped != 0 {
		t.Fatalf("expected zero queued stopped, got %d", result.QueuedStopped)
	}
}

func TestExecuteUserStopPlanRemovesOnlyTargetedQueuedTurn(t *testing.T) {
	roomID := id.RoomID("!room:test")
	oc := &AIClient{
		pendingQueues: map[id.RoomID]*pendingQueue{
			roomID: {
				items: []pendingQueueItem{
					{pending: pendingMessage{SourceEventID: id.EventID("$one")}},
					{pending: pendingMessage{SourceEventID: id.EventID("$two")}},
				},
			},
		},
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}

	result := oc.executeUserStopPlan(context.Background(), userStopRequest{
		Portal: portal,
	}, userStopPlan{
		Kind:          stopPlanKindQueued,
		Scope:         "turn",
		TargetKind:    "source_event",
		TargetEventID: id.EventID("$one"),
	})
	if result.QueuedStopped != 1 {
		t.Fatalf("expected one queued turn to stop, got %#v", result)
	}
	snapshot := oc.getQueueSnapshot(roomID)
	if snapshot == nil || len(snapshot.items) != 1 {
		t.Fatalf("expected one queued item to remain, got %#v", snapshot)
	}
	if got := snapshot.items[0].pending.sourceEventID(); got != id.EventID("$two") {
		t.Fatalf("expected remaining queued event $two, got %q", got)
	}
}

func TestExecuteUserStopPlanActiveNoOpFallsBackToNoMatch(t *testing.T) {
	roomID := id.RoomID("!room:test")
	oc := &AIClient{
		activeRoomRuns: map[id.RoomID]*roomRunState{
			roomID: {
				sourceEvent: id.EventID("$user"),
			},
		},
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{MXID: roomID}}

	result := oc.executeUserStopPlan(context.Background(), userStopRequest{
		Portal:  portal,
		ReplyTo: id.EventID("$user"),
	}, userStopPlan{
		Kind:          stopPlanKindActive,
		Scope:         "turn",
		TargetKind:    "source_event",
		TargetEventID: id.EventID("$user"),
	})
	if result.Plan.Kind != stopPlanKindNoMatch {
		t.Fatalf("expected no-match fallback for no-op active stop, got %#v", result.Plan)
	}
	if result.ActiveStopped {
		t.Fatalf("expected active stop to report false, got %#v", result)
	}
}

func TestBuildStreamUIMessageIncludesStopMetadata(t *testing.T) {
	oc := &AIClient{}
	conv := bridgesdk.NewConversation[*AIClient, *Config](context.Background(), nil, nil, bridgev2.EventSender{}, nil, nil)
	turn := conv.StartTurn(context.Background(), nil, &bridgesdk.SourceRef{EventID: "$user", SenderID: "@user:test"})
	turn.SetID("turn-stop")
	state := &streamingState{
		turn:          turn,
		finishReason:  "stop",
		responseID:    "resp_123",
		completedAtMs: 1,
	}
	state.stop.Store(&assistantStopMetadata{
		Reason:             "user_stop",
		Scope:              "turn",
		TargetKind:         "source_event",
		TargetEventID:      "$user",
		RequestedByEventID: "$stop",
		RequestedVia:       "command",
	})

	ui := oc.buildStreamUIMessage(state, nil, nil)
	metadata, ok := ui["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map, got %T", ui["metadata"])
	}
	stop, ok := metadata["stop"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested stop metadata, got %#v", metadata["stop"])
	}
	if stop["reason"] != "user_stop" || stop["requested_via"] != "command" {
		t.Fatalf("unexpected stop metadata: %#v", stop)
	}
	if metadata["response_status"] != "cancelled" {
		t.Fatalf("expected cancelled response status for stopped turn, got %#v", metadata["response_status"])
	}
}
