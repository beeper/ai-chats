package ai

import (
	"context"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/ai-chats/pkg/runtime"
)

type pendingQueueItem struct {
	pending          pendingMessage
	acceptedMessages []*database.Message
	messageID        string
	summaryLine      string
	enqueuedAt       int64
	prompt           string
	backlogAfter     bool
	allowDuplicate   bool
}

type pendingQueue struct {
	mu             sync.Mutex
	items          []pendingQueueItem
	draining       bool
	lastEnqueuedAt int64
	mode           airuntime.QueueMode
	debounceMs     int
	cap            int
	dropPolicy     airuntime.QueueDropPolicy
	droppedCount   int
	summaryLines   []string
	lastItem       *pendingQueueItem
}

type queueSummaryState struct {
	DropPolicy   airuntime.QueueDropPolicy
	DroppedCount int
	SummaryLines []string
}

type queueState[T any] struct {
	queueSummaryState
	Items []T
	Cap   int
}

func (pm pendingMessage) sourceEventID() id.EventID {
	if pm.SourceEventID != "" {
		return pm.SourceEventID
	}
	if pm.Event != nil {
		return pm.Event.ID
	}
	return ""
}

type pendingQueueDispatchCandidate struct {
	items         []pendingQueueItem
	summaryPrompt string
	collect       bool
	synthetic     bool
}

func (oc *AIClient) getPendingQueue(roomID id.RoomID, settings airuntime.QueueSettings) *pendingQueue {
	oc.pendingQueuesMu.Lock()
	queue := oc.pendingQueues[roomID]
	if queue == nil {
		queue = &pendingQueue{
			items:      []pendingQueueItem{},
			mode:       settings.Mode,
			debounceMs: settings.DebounceMs,
			cap:        settings.Cap,
			dropPolicy: settings.DropPolicy,
		}
		oc.pendingQueues[roomID] = queue
	}
	queue.mu.Lock()
	queue.mode = settings.Mode
	if settings.DebounceMs >= 0 {
		queue.debounceMs = settings.DebounceMs
	}
	if settings.Cap > 0 {
		queue.cap = settings.Cap
	}
	if settings.DropPolicy != "" {
		queue.dropPolicy = settings.DropPolicy
	}
	oc.pendingQueuesMu.Unlock()
	return queue
}

func (oc *AIClient) clearPendingQueue(ctx context.Context, roomID id.RoomID) {
	oc.finalizeStoppedQueueItems(ctx, oc.drainPendingQueue(roomID))
}

func (oc *AIClient) drainPendingQueue(roomID id.RoomID) []pendingQueueItem {
	if oc == nil || roomID == "" {
		return nil
	}
	oc.pendingQueuesMu.Lock()
	queue := oc.pendingQueues[roomID]
	if queue == nil {
		oc.pendingQueuesMu.Unlock()
		return nil
	}
	queue.mu.Lock()
	delete(oc.pendingQueues, roomID)
	items := queue.items
	queue.items = nil
	queue.lastItem = nil
	queue.mu.Unlock()
	oc.pendingQueuesMu.Unlock()

	oc.stopQueueTyping(roomID)
	return items
}

func (oc *AIClient) removePendingQueueBySourceEvent(roomID id.RoomID, sourceEventID id.EventID) []pendingQueueItem {
	if oc == nil || roomID == "" || sourceEventID == "" {
		return nil
	}
	oc.pendingQueuesMu.Lock()
	queue := oc.pendingQueues[roomID]
	if queue == nil {
		oc.pendingQueuesMu.Unlock()
		return nil
	}
	queue.mu.Lock()
	removed := make([]pendingQueueItem, 0, 1)
	kept := queue.items[:0]
	for _, item := range queue.items {
		if item.pending.sourceEventID() == sourceEventID {
			removed = append(removed, item)
			continue
		}
		kept = append(kept, item)
	}
	clear(queue.items[len(kept):])
	queue.items = kept
	if queue.lastItem != nil && queue.lastItem.pending.sourceEventID() == sourceEventID {
		queue.lastItem = nil
		if len(kept) > 0 {
			lastItem := kept[len(kept)-1]
			queue.lastItem = &lastItem
		}
	}
	empty := len(queue.items) == 0 && queue.droppedCount == 0
	if empty {
		delete(oc.pendingQueues, roomID)
	}
	queue.mu.Unlock()
	oc.pendingQueuesMu.Unlock()

	if empty {
		oc.stopQueueTyping(roomID)
	}
	return removed
}

func (oc *AIClient) enqueuePendingItem(roomID id.RoomID, item pendingQueueItem, settings airuntime.QueueSettings) bool {
	queue := oc.getPendingQueue(roomID, settings)
	if queue == nil {
		return false
	}
	defer queue.mu.Unlock()

	for _, existing := range queue.items {
		if pendingQueueItemsConflict(item, existing) {
			return false
		}
	}

	queue.lastEnqueuedAt = time.Now().UnixMilli()
	queue.lastItem = &item

	state := queueState[pendingQueueItem]{
		queueSummaryState: queueSummaryState{
			DropPolicy:   queue.dropPolicy,
			DroppedCount: queue.droppedCount,
			SummaryLines: queue.summaryLines,
		},
		Items: queue.items,
		Cap:   queue.cap,
	}
	shouldEnqueue := true
	if state.Cap > 0 && len(state.Items) >= state.Cap {
		overflow := airuntime.ResolveQueueOverflow(state.Cap, len(state.Items), state.DropPolicy)
		if !overflow.KeepNew {
			shouldEnqueue = false
		} else if dropCount := overflow.ItemsToDrop; dropCount >= 1 {
			dropped := state.Items[:dropCount]
			state.Items = state.Items[dropCount:]
			if overflow.ShouldSummarize {
				for _, entry := range dropped {
					state.DroppedCount++
					summary := entry.summaryLine
					if summary == "" {
						summary = strings.TrimSpace(entry.pending.MessageBody)
					}
					summary = strings.TrimSpace(summary)
					if summary != "" {
						state.SummaryLines = append(state.SummaryLines, airuntime.BuildQueueSummaryLine(summary, 160))
					}
				}
				if len(state.SummaryLines) > state.Cap {
					state.SummaryLines = state.SummaryLines[len(state.SummaryLines)-state.Cap:]
				}
			}
		}
	}
	queue.items = state.Items
	queue.droppedCount = state.DroppedCount
	queue.summaryLines = state.SummaryLines

	if !shouldEnqueue {
		oc.log.Debug().Stringer("room_id", roomID).Str("message_id", item.messageID).Msg("Pending queue item dropped by policy")
		return false
	}
	queue.items = append(queue.items, item)
	oc.log.Debug().Stringer("room_id", roomID).Str("message_id", item.messageID).Int("queue_size", len(queue.items)).Msg("Pending queue item enqueued")
	return true
}

func pendingQueueItemsConflict(item pendingQueueItem, existing pendingQueueItem) bool {
	if item.allowDuplicate {
		return false
	}
	if item.messageID != "" && existing.messageID == item.messageID {
		return true
	}
	return item.messageID == "" &&
		existing.messageID == "" &&
		item.pending.MessageBody != "" &&
		existing.pending.MessageBody == item.pending.MessageBody
}
