package ai

import (
	"slices"
	"strconv"
	"strings"

	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func (oc *AIClient) popQueueItems(roomID id.RoomID, count int) []pendingQueueItem {
	oc.pendingQueuesMu.Lock()
	defer oc.pendingQueuesMu.Unlock()
	queue := oc.pendingQueues[roomID]
	if queue == nil || len(queue.items) == 0 || count <= 0 {
		return nil
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if count > len(queue.items) {
		count = len(queue.items)
	}
	out := make([]pendingQueueItem, count)
	copy(out, queue.items[:count])
	queue.items = queue.items[count:]
	if len(queue.items) == 0 && queue.droppedCount == 0 {
		delete(oc.pendingQueues, roomID)
	}
	return out
}

func (oc *AIClient) getQueueSnapshot(roomID id.RoomID) *pendingQueue {
	oc.pendingQueuesMu.Lock()
	defer oc.pendingQueuesMu.Unlock()
	queue := oc.pendingQueues[roomID]
	if queue == nil {
		return nil
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	clone := &pendingQueue{
		items:          slices.Clone(queue.items),
		draining:       queue.draining,
		lastEnqueuedAt: queue.lastEnqueuedAt,
		mode:           queue.mode,
		debounceMs:     queue.debounceMs,
		cap:            queue.cap,
		dropPolicy:     queue.dropPolicy,
		droppedCount:   queue.droppedCount,
		summaryLines:   slices.Clone(queue.summaryLines),
	}
	if queue.lastItem != nil {
		lastItem := *queue.lastItem
		clone.lastItem = &lastItem
	}
	return clone
}

func (oc *AIClient) roomHasPendingQueueWork(roomID id.RoomID) bool {
	if oc == nil || roomID == "" {
		return false
	}
	oc.pendingQueuesMu.Lock()
	defer oc.pendingQueuesMu.Unlock()
	queue := oc.pendingQueues[roomID]
	if queue == nil {
		return false
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return queue.draining || len(queue.items) > 0 || queue.droppedCount > 0
}

func (oc *AIClient) consumeQueueSummary(roomID id.RoomID, noun string) string {
	oc.pendingQueuesMu.Lock()
	defer oc.pendingQueuesMu.Unlock()
	queue := oc.pendingQueues[roomID]
	if queue == nil || queue.droppedCount == 0 {
		return ""
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	summary := ""
	if queue.dropPolicy == airuntime.QueueDropSummarize && queue.droppedCount > 0 {
		title := "[Queue overflow] Dropped " + strconv.Itoa(queue.droppedCount) + " " + noun
		if queue.droppedCount != 1 {
			title += "s"
		}
		title += " due to cap."
		lines := []string{title}
		if len(queue.summaryLines) > 0 {
			lines = append(lines, "Summary:")
			for _, line := range queue.summaryLines {
				lines = append(lines, "- "+line)
			}
		}
		summary = strings.Join(lines, "\n")
	}
	queue.droppedCount = 0
	queue.summaryLines = nil
	if len(queue.items) == 0 {
		delete(oc.pendingQueues, roomID)
	}
	return summary
}
