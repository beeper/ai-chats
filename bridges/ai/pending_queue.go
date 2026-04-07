package ai

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
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
	summary := buildQueueSummaryPrompt(queue, noun)
	queue.droppedCount = 0
	queue.summaryLines = nil
	if len(queue.items) == 0 {
		delete(oc.pendingQueues, roomID)
	}
	return summary
}

func (oc *AIClient) takePendingQueueDispatchCandidate(roomID id.RoomID, textOnly bool) (*pendingQueueDispatchCandidate, *pendingQueue) {
	snapshot := oc.getQueueSnapshot(roomID)
	if snapshot == nil || (len(snapshot.items) == 0 && snapshot.droppedCount == 0) {
		return nil, snapshot
	}
	behavior := airuntime.ResolveQueueBehavior(snapshot.mode)

	if behavior.Collect && len(snapshot.items) > 0 {
		count := len(snapshot.items)
		if count > 1 {
			firstKey := oc.queueThreadKey(snapshot.items[0].pending.Event)
			for i := 1; i < count; i++ {
				if oc.queueThreadKey(snapshot.items[i].pending.Event) != firstKey {
					count = i
					break
				}
			}
		}
		if textOnly {
			for i := 0; i < count; i++ {
				if snapshot.items[i].pending.Type != pendingTypeText {
					return nil, snapshot
				}
			}
		}
		summary := ""
		if snapshot.droppedCount > 0 {
			summary = oc.consumeQueueSummary(roomID, "message")
		}
		items := oc.popQueueItems(roomID, count)
		for idx := range items {
			if items[idx].prompt == "" {
				items[idx].prompt = items[idx].pending.MessageBody
			}
		}
		return &pendingQueueDispatchCandidate{
			items:         items,
			summaryPrompt: summary,
			collect:       true,
		}, snapshot
	}

	if snapshot.dropPolicy == airuntime.QueueDropSummarize && snapshot.droppedCount > 0 {
		item := snapshot.items[0]
		if snapshot.lastItem != nil {
			item = *snapshot.lastItem
		}
		if textOnly && item.pending.Type != pendingTypeText {
			return nil, snapshot
		}
		return &pendingQueueDispatchCandidate{
			items:         []pendingQueueItem{item},
			summaryPrompt: oc.consumeQueueSummary(roomID, "message"),
			synthetic:     true,
		}, snapshot
	}

	if len(snapshot.items) == 0 {
		return nil, snapshot
	}
	if textOnly && snapshot.items[0].pending.Type != pendingTypeText {
		return nil, snapshot
	}
	items := oc.popQueueItems(roomID, 1)
	return &pendingQueueDispatchCandidate{items: items}, snapshot
}

func preparePendingQueueDispatchCandidate(candidate *pendingQueueDispatchCandidate) (pendingQueueItem, string, bool) {
	if candidate == nil || len(candidate.items) == 0 {
		return pendingQueueItem{}, "", false
	}
	if candidate.collect {
		items := candidate.items
		ackIDs := make([]id.EventID, 0, len(items))
		for idx := range items {
			if items[idx].pending.Event != nil {
				if len(items[idx].pending.AckEventIDs) > 0 {
					ackIDs = append(ackIDs, items[idx].pending.AckEventIDs...)
				} else {
					ackIDs = append(ackIDs, items[idx].pending.Event.ID)
				}
			}
			if items[idx].prompt == "" {
				items[idx].prompt = items[idx].pending.MessageBody
			}
		}
		item := items[len(items)-1]
		if len(ackIDs) > 0 {
			item.pending.AckEventIDs = ackIDs
		}
		return item, buildCollectPrompt("[Queued messages while agent was busy]", items, candidate.summaryPrompt), true
	}

	item := candidate.items[0]
	if candidate.summaryPrompt != "" && candidate.synthetic {
		item.pending.Event = nil
		item.pending.MessageBody = candidate.summaryPrompt
		item.backlogAfter = false
		item.allowDuplicate = false
		return item, candidate.summaryPrompt, true
	}
	return item, strings.TrimSpace(item.pending.MessageBody), true
}

func (oc *AIClient) getSteeringMessages(roomID id.RoomID) []string {
	if oc == nil || roomID == "" {
		return nil
	}
	steerItems := oc.drainSteerQueue(roomID)
	if len(steerItems) == 0 {
		return nil
	}

	messages := make([]string, 0, len(steerItems))
	for _, item := range steerItems {
		if item.pending.Type != pendingTypeText {
			continue
		}
		prompt := strings.TrimSpace(item.prompt)
		if prompt == "" {
			prompt = item.pending.MessageBody
		}
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			continue
		}
		messages = append(messages, prompt)
	}
	return messages
}

func buildSteeringPromptMessages(prompts []string) []PromptMessage {
	if len(prompts) == 0 {
		return nil
	}
	messages := make([]PromptMessage, 0, len(prompts))
	for _, prompt := range prompts {
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			continue
		}
		messages = append(messages, newUserTextPromptMessage(prompt))
	}
	return messages
}

func (oc *AIClient) markQueueDraining(roomID id.RoomID) bool {
	oc.pendingQueuesMu.Lock()
	defer oc.pendingQueuesMu.Unlock()
	queue := oc.pendingQueues[roomID]
	if queue == nil || queue.draining {
		return false
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.draining {
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
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.draining = false
	if len(queue.items) == 0 && queue.droppedCount == 0 {
		delete(oc.pendingQueues, roomID)
	}
}

func (oc *AIClient) dispatchQueuedPrompt(
	ctx context.Context,
	item pendingQueueItem,
	promptContext PromptContext,
) {
	var roomID id.RoomID
	if item.pending.Portal != nil {
		roomID = item.pending.Portal.MXID
	}
	oc.log.Debug().Stringer("room_id", roomID).Str("message_id", item.messageID).Int("prompt_len", len(promptContext.Messages)).Msg("Dispatching queued prompt")
	runCtx := oc.attachRoomRun(ctx, roomID)
	runCtx = context.WithValue(runCtx, queueAcceptedStatusKey{}, true)
	if item.pending.InboundContext != nil {
		runCtx = withInboundContext(runCtx, *item.pending.InboundContext)
	}
	if item.pending.Typing != nil {
		runCtx = WithTypingContext(runCtx, item.pending.Typing)
	}
	metaSnapshot := clonePortalMetadata(item.pending.Meta)
	go func() {
		defer func() {
			oc.removePendingAckReactions(oc.backgroundContext(ctx), item.pending.Portal, item.pending)
			if item.backlogAfter {
				followup := item
				followup.backlogAfter = false
				followup.allowDuplicate = true
				queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(oc.backgroundContext(ctx), item.pending.Portal, item.pending.Meta, "", airuntime.QueueInlineOptions{})
				oc.queuePendingMessage(roomID, followup, queueSettings)
			}
			oc.releaseRoom(roomID)
			oc.processPendingQueue(oc.backgroundContext(ctx), roomID)
		}()
		oc.dispatchCompletionInternal(runCtx, item.pending.Event, item.pending.Portal, metaSnapshot, promptContext)
	}()
}

func (oc *AIClient) removePendingAckReactions(ctx context.Context, portal *bridgev2.Portal, pending pendingMessage) {
	if portal == nil || pending.Meta == nil || !pending.Meta.AckReactionRemoveAfter {
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
