package ai

import (
	"context"
	"strconv"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func (oc *AIClient) takePendingQueueDispatchCandidate(roomID id.RoomID, textOnly bool) *pendingQueueDispatchCandidate {
	snapshot := oc.getQueueSnapshot(roomID)
	if snapshot == nil || (len(snapshot.items) == 0 && snapshot.droppedCount == 0) {
		return nil
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
					return nil
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
		}
	}

	if snapshot.dropPolicy == airuntime.QueueDropSummarize && snapshot.droppedCount > 0 {
		var item pendingQueueItem
		if snapshot.lastItem != nil {
			item = *snapshot.lastItem
		} else if len(snapshot.items) > 0 {
			item = snapshot.items[0]
		} else {
			return nil
		}
		if textOnly && item.pending.Type != pendingTypeText {
			return nil
		}
		return &pendingQueueDispatchCandidate{
			items:         []pendingQueueItem{item},
			summaryPrompt: oc.consumeQueueSummary(roomID, "message"),
			synthetic:     true,
		}
	}

	if len(snapshot.items) == 0 {
		return nil
	}
	if textOnly && snapshot.items[0].pending.Type != pendingTypeText {
		return nil
	}
	items := oc.popQueueItems(roomID, 1)
	return &pendingQueueDispatchCandidate{items: items}
}

func preparePendingQueueDispatchCandidate(candidate *pendingQueueDispatchCandidate) (pendingQueueItem, string, bool) {
	if candidate == nil || len(candidate.items) == 0 {
		return pendingQueueItem{}, "", false
	}
	if candidate.collect {
		items := candidate.items
		ackIDs := make([]id.EventID, 0, len(items))
		acceptedMessages := make([]*database.Message, 0, len(items))
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
			acceptedMessages = append(acceptedMessages, items[idx].acceptedMessages...)
		}
		item := items[len(items)-1]
		if len(ackIDs) > 0 {
			item.pending.AckEventIDs = ackIDs
		}
		item.acceptedMessages = acceptedMessages
		blocks := []string{"[Queued messages while the assistant was busy]"}
		if strings.TrimSpace(candidate.summaryPrompt) != "" {
			blocks = append(blocks, candidate.summaryPrompt)
		}
		for idx, queuedItem := range items {
			blocks = append(blocks, strings.TrimSpace("---\nQueued #"+strconv.Itoa(idx+1)+"\n"+queuedItem.prompt))
		}
		return item, strings.Join(blocks, "\n\n"), true
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
		messages = append(messages, PromptMessage{
			Role: PromptRoleUser,
			Blocks: []PromptBlock{{
				Type: PromptBlockText,
				Text: prompt,
			}},
		})
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
