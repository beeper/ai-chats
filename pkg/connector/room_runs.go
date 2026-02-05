package connector

import (
	"context"
	"sync"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type roomRunState struct {
	cancel context.CancelFunc

	mu           sync.Mutex
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
	cancel := (context.CancelFunc)(nil)
	if run != nil {
		cancel = run.cancel
	}
	if cancel != nil {
		cancel()
		return true
	}
	return false
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
	ackPending := append([]pendingMessage(nil), run.ackPending...)
	run.mu.Unlock()
	if len(ackPending) == 0 {
		return
	}
	ctx := oc.backgroundContext(context.Background())
	for _, pending := range ackPending {
		if pending.Meta != nil && pending.Meta.AckReactionRemoveAfter {
			oc.removePendingAckReactions(ctx, pending.Portal, pending)
		}
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
		if !item.allowDuplicate {
			if item.messageID != "" && existing.messageID == item.messageID {
				return false
			}
			if item.messageID == "" && existing.messageID == "" && item.pending.MessageBody != "" && existing.pending.MessageBody == item.pending.MessageBody {
				return false
			}
		}
	}
	run.steerQueue = append(run.steerQueue, item)
	if item.pending.Event != nil {
		run.statusEvents = append(run.statusEvents, item.pending.Event)
	}
	if len(item.pending.StatusEvents) > 0 {
		run.statusEvents = append(run.statusEvents, item.pending.StatusEvents...)
	}
	if item.pending.Meta != nil && item.pending.Meta.AckReactionRemoveAfter {
		run.ackPending = append(run.ackPending, item.pending)
	}
	return true
}

func (oc *AIClient) drainSteerQueue(roomID id.RoomID) []pendingQueueItem {
	run := oc.getRoomRun(roomID)
	if run == nil {
		return nil
	}
	run.mu.Lock()
	items := append([]pendingQueueItem(nil), run.steerQueue...)
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
	events := append([]*event.Event(nil), run.statusEvents...)
	run.mu.Unlock()
	return events
}
