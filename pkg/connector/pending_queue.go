package connector

import (
	"context"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

type pendingQueueItem struct {
	pending         pendingMessage
	messageID       string
	summaryLine     string
	enqueuedAt      int64
	rawEventContent map[string]any
	prompt          string
	backlogAfter    bool
	allowDuplicate  bool
}

type pendingQueue struct {
	items          []pendingQueueItem
	draining       bool
	lastEnqueuedAt int64
	mode           QueueMode
	debounceMs     int
	cap            int
	dropPolicy     QueueDropPolicy
	droppedCount   int
	summaryLines   []string
	lastItem       *pendingQueueItem
}

func (oc *AIClient) getPendingQueue(roomID id.RoomID, settings QueueSettings) *pendingQueue {
	oc.pendingQueuesMu.Lock()
	defer oc.pendingQueuesMu.Unlock()
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
	} else {
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
	}
	return queue
}

func (oc *AIClient) clearPendingQueue(roomID id.RoomID) {
	oc.pendingQueuesMu.Lock()
	_, existed := oc.pendingQueues[roomID]
	delete(oc.pendingQueues, roomID)
	oc.pendingQueuesMu.Unlock()
	if existed {
		oc.stopQueueTyping(roomID)
	}
}

func (oc *AIClient) enqueuePendingItem(roomID id.RoomID, item pendingQueueItem, settings QueueSettings) bool {
	queue := oc.getPendingQueue(roomID, settings)
	if queue == nil {
		return false
	}

	for _, existing := range queue.items {
		if !item.allowDuplicate {
			if item.messageID != "" && existing.messageID == item.messageID {
				return false
			}
			if item.messageID == "" && existing.messageID == "" && item.pending.MessageBody != "" && existing.pending.MessageBody == item.pending.MessageBody {
				return false
			}
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
	shouldEnqueue := applyQueueDropPolicy[pendingQueueItem](struct {
		Queue        *queueState[pendingQueueItem]
		Summarize    func(item pendingQueueItem) string
		SummaryLimit int
	}{
		Queue: &state,
		Summarize: func(entry pendingQueueItem) string {
			if entry.summaryLine != "" {
				return entry.summaryLine
			}
			return strings.TrimSpace(entry.pending.MessageBody)
		},
	})
	queue.items = state.Items
	queue.droppedCount = state.DroppedCount
	queue.summaryLines = state.SummaryLines

	if !shouldEnqueue {
		return false
	}
	queue.items = append(queue.items, item)
	return true
}

func (oc *AIClient) popQueueItems(roomID id.RoomID, count int) []pendingQueueItem {
	oc.pendingQueuesMu.Lock()
	defer oc.pendingQueuesMu.Unlock()
	queue := oc.pendingQueues[roomID]
	if queue == nil || len(queue.items) == 0 || count <= 0 {
		return nil
	}
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
	clone := *queue
	clone.items = append([]pendingQueueItem(nil), queue.items...)
	clone.summaryLines = append([]string(nil), queue.summaryLines...)
	return &clone
}

func (oc *AIClient) takeQueueSummary(roomID id.RoomID, noun string) string {
	oc.pendingQueuesMu.Lock()
	defer oc.pendingQueuesMu.Unlock()
	queue := oc.pendingQueues[roomID]
	if queue == nil {
		return ""
	}
	return buildQueueSummaryPrompt(queue, noun)
}

func (oc *AIClient) markQueueDraining(roomID id.RoomID) bool {
	oc.pendingQueuesMu.Lock()
	defer oc.pendingQueuesMu.Unlock()
	queue := oc.pendingQueues[roomID]
	if queue == nil || queue.draining {
		return false
	}
	queue.draining = true
	return true
}

func (oc *AIClient) clearQueueDraining(roomID id.RoomID) {
	oc.pendingQueuesMu.Lock()
	defer oc.pendingQueuesMu.Unlock()
	queue := oc.pendingQueues[roomID]
	if queue == nil {
		return
	}
	queue.draining = false
	if len(queue.items) == 0 && queue.droppedCount == 0 {
		delete(oc.pendingQueues, roomID)
	}
}

func (oc *AIClient) dispatchQueuedPrompt(
	ctx context.Context,
	item pendingQueueItem,
	prompt []openai.ChatCompletionMessageParamUnion,
) {
	roomID := id.RoomID("")
	if item.pending.Portal != nil {
		roomID = item.pending.Portal.MXID
	}
	runCtx := oc.attachRoomRun(ctx, roomID)
	if item.pending.Typing != nil {
		runCtx = WithTypingContext(runCtx, item.pending.Typing)
	}
	metaSnapshot := clonePortalMetadata(item.pending.Meta)
	go func() {
		defer func() {
			if metaSnapshot != nil && metaSnapshot.AckReactionRemoveAfter {
				oc.removePendingAckReactions(oc.backgroundContext(ctx), item.pending.Portal, item.pending)
			}
			if item.backlogAfter {
				followup := item
				followup.backlogAfter = false
				followup.allowDuplicate = true
				queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(oc.backgroundContext(ctx), item.pending.Portal, item.pending.Meta, "", QueueInlineOptions{})
				oc.queuePendingMessage(roomID, followup, queueSettings)
			}
			oc.releaseRoom(roomID)
			oc.processPendingQueue(oc.backgroundContext(ctx), roomID)
		}()
		oc.dispatchCompletionInternal(runCtx, item.pending.Event, item.pending.Portal, metaSnapshot, prompt)
	}()
}

func (oc *AIClient) removePendingAckReactions(ctx context.Context, portal *bridgev2.Portal, pending pendingMessage) {
	if portal == nil {
		return
	}
	ids := pending.AckEventIDs
	if len(ids) == 0 && pending.Event != nil {
		ids = []id.EventID{pending.Event.ID}
	}
	for _, sourceID := range ids {
		if sourceID != "" {
			oc.removeAckReaction(ctx, portal, sourceID)
		}
	}
}
