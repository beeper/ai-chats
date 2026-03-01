package connector

import (
	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
	"maunium.net/go/mautrix/id"
)

func queueModeToRuntime(mode QueueMode) airuntime.QueueMode {
	switch mode {
	case QueueModeInterrupt:
		return airuntime.QueueModeInterrupt
	case QueueModeSteer:
		return airuntime.QueueModeSteer
	case QueueModeFollowup:
		return airuntime.QueueModeFollowup
	case QueueModeCollect:
		return airuntime.QueueModeCollect
	case QueueModeSteerBacklog:
		return airuntime.QueueModeSteerBacklog
	default:
		return airuntime.QueueModeBacklog
	}
}

func (oc *AIClient) roomHasActiveRun(roomID id.RoomID) bool {
	if oc == nil || roomID == "" {
		return false
	}
	oc.activeRoomsMu.Lock()
	defer oc.activeRoomsMu.Unlock()
	return oc.activeRooms[roomID]
}

func (oc *AIClient) decideQueuePolicy(roomID id.RoomID, mode QueueMode, isHeartbeat bool) airuntime.QueueDecision {
	return airuntime.DecideQueueAction(queueModeToRuntime(mode), oc.roomHasActiveRun(roomID), isHeartbeat)
}

func (oc *AIClient) queueBehavior(mode QueueMode) airuntime.QueueBehavior {
	return airuntime.ResolveQueueBehavior(queueModeToRuntime(mode))
}
