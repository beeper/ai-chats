package ai

import (
	"testing"

	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func TestDecideQueuePolicy_InterruptWithActiveRun(t *testing.T) {
	client := &AIClient{
		activeRoomRuns: map[id.RoomID]*roomRunState{
			"!room:test": {},
		},
	}
	decision := airuntime.DecideQueueAction(airuntime.QueueModeInterrupt, client.roomHasActiveRun("!room:test"), false)
	if decision.Action != airuntime.QueueActionInterruptAndRun {
		t.Fatalf("expected interrupt decision, got %#v", decision)
	}
}

func TestDecideQueuePolicy_BacklogWithoutActiveRun(t *testing.T) {
	client := &AIClient{activeRoomRuns: map[id.RoomID]*roomRunState{}}
	decision := airuntime.DecideQueueAction(airuntime.QueueModeCollect, client.roomHasActiveRun("!room:test"), false)
	if decision.Action != airuntime.QueueActionRunNow {
		t.Fatalf("expected run-now without active run, got %#v", decision)
	}
}
