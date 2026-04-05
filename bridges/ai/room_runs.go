package ai

import (
	"context"
	"slices"
	"sync"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type roomRunState struct {
	cancel context.CancelFunc

	mu           sync.Mutex
	state        *streamingState
	stop         *assistantStopMetadata
	turnID       string
	sourceEvent  id.EventID
	initialEvent id.EventID
	streaming    bool
	steerQueue   []pendingQueueItem
	statusEvents []*event.Event
	ackPending   []pendingMessage
}

func (oc *AIClient) attachRoomRun(ctx context.Context, roomID id.RoomID) context.Context {
	if oc == nil || roomID == "" {
		return ctx
	}
	runCtx, cancel := context.WithCancel(ctx)
	oc.activeRoomRunsMu.Lock()
	if oc.activeRoomRuns == nil {
		oc.activeRoomRuns = make(map[id.RoomID]*roomRunState)
	}
	oc.activeRoomRuns[roomID] = &roomRunState{cancel: cancel}
	oc.activeRoomRunsMu.Unlock()
	return runCtx
}

func (oc *AIClient) cancelRoomRun(roomID id.RoomID) bool {
	if oc == nil || roomID == "" {
		return false
	}
	oc.activeRoomRunsMu.Lock()
	run := oc.activeRoomRuns[roomID]
	oc.activeRoomRunsMu.Unlock()
	if run == nil || run.cancel == nil {
		return false
	}
	run.cancel()
	return true
}

func (oc *AIClient) clearRoomRun(roomID id.RoomID) {
	if oc == nil || roomID == "" {
		return
	}
	oc.activeRoomRunsMu.Lock()
	run := oc.activeRoomRuns[roomID]
	if run != nil {
		delete(oc.activeRoomRuns, roomID)
	}
	oc.activeRoomRunsMu.Unlock()
	if run == nil {
		return
	}
	if run.cancel != nil {
		run.cancel()
	}
	run.mu.Lock()
	ackPending := slices.Clone(run.ackPending)
	run.mu.Unlock()
	if len(ackPending) == 0 {
		return
	}
	ctx := oc.backgroundContext(context.Background())
	for _, pending := range ackPending {
		oc.removePendingAckReactions(ctx, pending.Portal, pending)
	}
}

func (oc *AIClient) getRoomRun(roomID id.RoomID) *roomRunState {
	if oc == nil || roomID == "" {
		return nil
	}
	oc.activeRoomRunsMu.Lock()
	run := oc.activeRoomRuns[roomID]
	oc.activeRoomRunsMu.Unlock()
	return run
}

func (oc *AIClient) markRoomRunStreaming(roomID id.RoomID, streaming bool) {
	run := oc.getRoomRun(roomID)
	if run == nil {
		return
	}
	run.mu.Lock()
	run.streaming = streaming
	run.mu.Unlock()
}

func (oc *AIClient) bindRoomRunState(roomID id.RoomID, state *streamingState) {
	run := oc.getRoomRun(roomID)
	if run == nil {
		return
	}
	run.mu.Lock()
	run.state = state
	if run.stop != nil && state != nil {
		state.stop.Store(run.stop)
	}
	if state != nil && state.turn != nil {
		run.turnID = state.turn.ID()
		run.sourceEvent = state.sourceEventID()
		run.initialEvent = state.turn.InitialEventID()
	}
	run.mu.Unlock()
}

func (oc *AIClient) roomRunTarget(roomID id.RoomID) (turnID string, sourceEventID, initialEventID id.EventID, state *streamingState) {
	run := oc.getRoomRun(roomID)
	if run == nil {
		return "", "", "", nil
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	state = run.state
	if state != nil && state.turn != nil {
		return state.turn.ID(), state.sourceEventID(), state.turn.InitialEventID(), state
	}
	return run.turnID, run.sourceEvent, run.initialEvent, state
}

func (oc *AIClient) markRoomRunStopped(roomID id.RoomID, stop *assistantStopMetadata) bool {
	run := oc.getRoomRun(roomID)
	if run == nil || stop == nil {
		return false
	}
	run.mu.Lock()
	run.stop = stop
	if run.state != nil {
		run.state.stop.Store(stop)
	}
	run.mu.Unlock()
	return true
}

func (oc *AIClient) enqueueSteerQueue(roomID id.RoomID, item pendingQueueItem) bool {
	run := oc.getRoomRun(roomID)
	if run == nil {
		return false
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if !run.streaming {
		return false
	}
	for _, existing := range run.steerQueue {
		if pendingQueueItemsConflict(item, existing) {
			return false
		}
	}
	run.steerQueue = append(run.steerQueue, item)
	oc.registerRoomRunPendingItemLocked(run, item)
	return true
}

func (oc *AIClient) registerRoomRunPendingItemLocked(run *roomRunState, item pendingQueueItem) {
	if run == nil {
		return
	}
	if item.pending.Event != nil {
		run.statusEvents = append(run.statusEvents, item.pending.Event)
	}
	if len(item.pending.StatusEvents) > 0 {
		run.statusEvents = append(run.statusEvents, item.pending.StatusEvents...)
	}
	if item.pending.Meta != nil && item.pending.Meta.AckReactionRemoveAfter {
		run.ackPending = append(run.ackPending, item.pending)
	}
}

func (oc *AIClient) drainSteerQueue(roomID id.RoomID) []pendingQueueItem {
	run := oc.getRoomRun(roomID)
	if run == nil {
		return nil
	}
	run.mu.Lock()
	items := slices.Clone(run.steerQueue)
	run.steerQueue = nil
	run.mu.Unlock()
	return items
}

func (oc *AIClient) roomRunStatusEvents(roomID id.RoomID) []*event.Event {
	run := oc.getRoomRun(roomID)
	if run == nil {
		return nil
	}
	run.mu.Lock()
	events := slices.Clone(run.statusEvents)
	run.mu.Unlock()
	return events
}
