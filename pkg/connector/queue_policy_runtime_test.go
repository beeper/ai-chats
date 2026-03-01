package connector

import (
	"testing"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
	"maunium.net/go/mautrix/id"
)

func TestDecideQueuePolicy_InterruptWithActiveRun(t *testing.T) {
	client := &AIClient{
		activeRooms: map[id.RoomID]bool{
			"!room:test": true,
		},
	}
	decision := airuntime.DecideQueueAction(airuntime.QueueModeInterrupt, client.roomHasActiveRun("!room:test"), false)
	if decision.Action != airuntime.QueueActionInterruptAndRun {
		t.Fatalf("expected interrupt decision, got %#v", decision)
	}
}

func TestDecideQueuePolicy_BacklogWithoutActiveRun(t *testing.T) {
	client := &AIClient{activeRooms: map[id.RoomID]bool{}}
	decision := airuntime.DecideQueueAction(airuntime.QueueModeCollect, client.roomHasActiveRun("!room:test"), false)
	if decision.Action != airuntime.QueueActionRunNow {
		t.Fatalf("expected run-now without active run, got %#v", decision)
	}
}
