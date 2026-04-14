package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
)

func (oc *AIClient) roomHasActiveRun(roomID id.RoomID) bool {
	return oc.getRoomRun(roomID) != nil
}

func (oc *AIClient) hasInflightRequests() bool {
	if oc == nil {
		return false
	}

	oc.activeRoomRunsMu.Lock()
	active := false
	for _, run := range oc.activeRoomRuns {
		if run != nil {
			active = true
			break
		}
	}
	oc.activeRoomRunsMu.Unlock()
	if active {
		return true
	}

	oc.pendingQueuesMu.Lock()
	defer oc.pendingQueuesMu.Unlock()
	for _, queue := range oc.pendingQueues {
		if queue != nil && (len(queue.items) > 0 || queue.droppedCount > 0) {
			return true
		}
	}
	return false
}

func (oc *AIClient) acquireRoom(roomID id.RoomID) bool {
	oc.roomLocksMu.Lock()
	defer oc.roomLocksMu.Unlock()
	if oc.roomLocks[roomID] {
		return false
	}
	oc.roomLocks[roomID] = true
	return true
}

// releaseRoom releases a room after processing is complete.
func (oc *AIClient) releaseRoom(roomID id.RoomID) {
	oc.roomLocksMu.Lock()
	defer oc.roomLocksMu.Unlock()
	delete(oc.roomLocks, roomID)
	oc.clearRoomRun(roomID)
}

// queuePendingMessage adds a message to the pending queue for later processing.
func (oc *AIClient) queuePendingMessage(roomID id.RoomID, item pendingQueueItem, settings airuntime.QueueSettings) bool {
	enqueued := oc.enqueuePendingItem(roomID, item, settings)
	if enqueued {
		oc.startQueueTyping(oc.backgroundContext(context.Background()), item.pending.Portal, item.pending.Meta, item.pending.Typing)
	}
	return enqueued
}

func queueStatusEvents(primary *event.Event, extras []*event.Event) []*event.Event {
	events := make([]*event.Event, 0, 1+len(extras))
	seen := make(map[id.EventID]struct{}, 1+len(extras))
	appendEvent := func(evt *event.Event) {
		if evt == nil || evt.ID == "" {
			return
		}
		if _, exists := seen[evt.ID]; exists {
			return
		}
		seen[evt.ID] = struct{}{}
		events = append(events, evt)
	}
	appendEvent(primary)
	for _, evt := range extras {
		appendEvent(evt)
	}
	return events
}

func (oc *AIClient) sendQueueRejectedStatus(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, extras []*event.Event, reason string) {
	if portal == nil || portal.Bridge == nil {
		return
	}
	message := strings.TrimSpace(reason)
	if message == "" {
		message = "Couldn't queue the message. Try again."
	}
	err := fmt.Errorf("%s", message)
	msgStatus := bridgev2.WrapErrorInStatus(err).
		WithStatus(event.MessageStatusRetriable).
		WithErrorReason(event.MessageStatusGenericError).
		WithMessage(message).
		WithIsCertain(true).
		WithSendNotice(false)
	for _, statusEvt := range queueStatusEvents(evt, extras) {
		bridgeutil.SendMessageStatus(ctx, portal, statusEvt, msgStatus)
	}
}

func (oc *AIClient) dispatchPromptRun(
	ctx context.Context,
	roomID id.RoomID,
	item pendingQueueItem,
	promptContext PromptContext,
	queueAccepted bool,
) {
	runCtx := oc.attachRoomRun(oc.backgroundContext(ctx), roomID)
	if queueAccepted {
		runCtx = context.WithValue(runCtx, queueAcceptedStatusKey{}, true)
	}
	if len(item.pending.StatusEvents) > 0 {
		runCtx = context.WithValue(runCtx, statusEventsKey{}, item.pending.StatusEvents)
	}
	if item.pending.InboundContext != nil {
		runCtx = withInboundContext(runCtx, *item.pending.InboundContext)
	}
	if item.pending.Typing != nil {
		runCtx = WithTypingContext(runCtx, item.pending.Typing)
	}
	metaSnapshot := clonePortalMetadata(item.pending.Meta)
	go func(metaSnapshot *PortalMetadata) {
		defer func() {
			oc.removePendingAckReactions(oc.backgroundContext(ctx), item.pending.Portal, item.pending)
			if item.backlogAfter {
				followup := item
				followup.backlogAfter = false
				followup.allowDuplicate = true
				var cfg *Config
				if oc != nil && oc.connector != nil {
					cfg = &oc.connector.Config
				}
				queueSettings := resolveQueueSettings(queueResolveParams{cfg: cfg, channel: "matrix", inlineOpts: airuntime.QueueInlineOptions{}})
				oc.queuePendingMessage(roomID, followup, queueSettings)
			}
			oc.releaseRoom(roomID)
			oc.processPendingQueue(oc.backgroundContext(ctx), roomID)
		}()
		oc.dispatchCompletionInternal(runCtx, item.pending.Event, item.pending.Portal, metaSnapshot, promptContext)
	}(metaSnapshot)
}

// dispatchOrQueueCore contains shared dispatch/steer/queue logic.
// When userMessage is non-nil, it saves the message to the DB, handles ack
// reactions, sends pending status on acquire, and notifies session mutations.
// Returns true if the message was accepted (dispatched or queued).
func (oc *AIClient) dispatchOrQueueCore(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	userMessage *database.Message,
	queueItem pendingQueueItem,
	queueSettings airuntime.QueueSettings,
	promptContext PromptContext,
) bool {
	roomID := portal.MXID
	behavior := airuntime.ResolveQueueBehavior(queueSettings.Mode)
	shouldSteer := behavior.Steer
	shouldFollowup := behavior.Followup
	hasDBMessage := userMessage != nil
	roomBusy := oc.roomHasActiveRun(roomID) || oc.roomHasPendingQueueWork(roomID)
	queueDecision := airuntime.DecideQueueAction(queueSettings.Mode, roomBusy, false)
	if queueDecision.Action == airuntime.QueueActionInterruptAndRun {
		oc.cancelRoomRun(roomID)
		oc.clearPendingQueue(ctx, roomID)
		roomBusy = false
	}
	if !roomBusy && oc.acquireRoom(roomID) {
		oc.stopQueueTyping(roomID)
		if hasDBMessage {
			oc.saveUserMessage(ctx, evt, userMessage)
		}
		if evt != nil && !queueItem.pending.PendingSent {
			bridgeutil.SendMessageStatus(ctx, portal, evt, bridgev2.MessageStatus{
				Status:    event.MessageStatusPending,
				Message:   "Processing...",
				IsCertain: true,
			})
			queueItem.pending.PendingSent = true
		}
		queuedItem := queueItem
		queuedItem.pending.Portal = portal
		queuedItem.pending.Meta = meta
		queuedItem.pending.Event = evt
		oc.dispatchPromptRun(ctx, roomID, queuedItem, promptContext, false)
		if hasDBMessage {
			oc.notifySessionMutation(ctx, portal, meta, false)
		}
		return true
	}

	messageSaved := false
	if shouldSteer && queueItem.pending.Type == pendingTypeText {
		queueItem.prompt = queueItem.pending.MessageBody
		steered := oc.enqueueSteerQueue(roomID, queueItem)
		if steered {
			if hasDBMessage {
				oc.saveUserMessage(ctx, evt, userMessage)
				messageSaved = true
			}
			if !shouldFollowup {
				if evt != nil && !queueItem.pending.PendingSent {
					bridgeutil.SendMessageStatus(ctx, portal, evt, bridgev2.MessageStatus{
						Status:    event.MessageStatusPending,
						Message:   "Processing...",
						IsCertain: true,
					})
					queueItem.pending.PendingSent = true
				}
				if hasDBMessage {
					oc.notifySessionMutation(ctx, portal, meta, false)
				}
				return true
			}
		}
	}

	if behavior.BacklogAfter {
		queueItem.backlogAfter = true
	}
	enqueued := oc.queuePendingMessage(roomID, queueItem, queueSettings)
	if !enqueued {
		oc.sendQueueRejectedStatus(ctx, portal, evt, queueItem.pending.StatusEvents, "Couldn't queue the message. Try again.")
		return false
	}
	for _, statusEvt := range queueStatusEvents(evt, queueItem.pending.StatusEvents) {
		bridgeutil.SendMessageStatus(ctx, portal, statusEvt, bridgev2.MessageStatus{
			Status:    event.MessageStatusSuccess,
			IsCertain: true,
		})
	}
	if hasDBMessage && !messageSaved {
		oc.saveUserMessage(ctx, evt, userMessage)
	}
	if hasDBMessage {
		oc.notifySessionMutation(ctx, portal, meta, false)
	}
	return true
}

// processPendingQueue processes queued messages for a room.
func (oc *AIClient) processPendingQueue(ctx context.Context, roomID id.RoomID) {
	if oc == nil || roomID == "" {
		return
	}
	if !oc.markQueueDraining(roomID) {
		return
	}

	go func() {
		defer oc.clearQueueDraining(roomID)
		snapshot := oc.getQueueSnapshot(roomID)
		if snapshot == nil || (len(snapshot.items) == 0 && snapshot.droppedCount == 0) {
			return
		}
		if snapshot.debounceMs > 0 {
			for {
				current := oc.getQueueSnapshot(roomID)
				if current == nil {
					return
				}
				since := time.Now().UnixMilli() - current.lastEnqueuedAt
				if since >= int64(current.debounceMs) {
					break
				}
				wait := current.debounceMs - int(since)
				if wait < 0 {
					wait = 0
				}
				time.Sleep(time.Duration(wait) * time.Millisecond)
			}
		}

		if !oc.acquireRoom(roomID) {
			return
		}
		oc.stopQueueTyping(roomID)

		candidate, actionSnapshot := oc.takePendingQueueDispatchCandidate(roomID, false)
		if actionSnapshot == nil || candidate == nil || len(candidate.items) == 0 {
			oc.releaseRoom(roomID)
			return
		}

		item, prompt, ok := preparePendingQueueDispatchCandidate(candidate)
		if !ok {
			oc.releaseRoom(roomID)
			return
		}

		var promptContext PromptContext
		var err error

		metaSnapshot := clonePortalMetadata(item.pending.Meta)
		var eventID id.EventID
		if item.pending.Event != nil {
			eventID = item.pending.Event.ID
		}
		promptCtx := ctx
		if item.pending.InboundContext != nil {
			promptCtx = withInboundContext(promptCtx, *item.pending.InboundContext)
		}
		switch item.pending.Type {
		case pendingTypeText:
			promptContext, err = oc.buildPromptContextForTurn(promptCtx, item.pending.Portal, metaSnapshot, prompt, eventID, currentTurnPromptOptions{
				currentTurnTextOptions: currentTurnTextOptions{
					rawEventContent:  item.rawEventContent,
					includeLinkScope: true,
				},
			})
		case pendingTypeImage, pendingTypePDF, pendingTypeAudio, pendingTypeVideo:
			promptContext, err = oc.buildPromptContextForTurn(promptCtx, item.pending.Portal, metaSnapshot, item.pending.MessageBody, eventID, currentTurnPromptOptions{
				currentTurnTextOptions: currentTurnTextOptions{includeLinkScope: true},
				attachment: &turnAttachmentOptions{
					mediaURL:      item.pending.MediaURL,
					mimeType:      item.pending.MimeType,
					encryptedFile: item.pending.EncryptedFile,
					mediaType:     item.pending.Type,
				},
			})
		case pendingTypeRegenerate:
			promptContext, err = oc.buildContextForRegenerate(promptCtx, item.pending.Portal, metaSnapshot, item.pending.MessageBody, item.pending.SourceEventID)
		case pendingTypeEditRegenerate:
			promptContext, err = oc.buildContextUpToMessage(promptCtx, item.pending.Portal, metaSnapshot, item.pending.TargetMsgID, item.pending.MessageBody)
		default:
			err = fmt.Errorf("unknown pending message type: %s", item.pending.Type)
		}

		if err != nil {
			oc.loggerForContext(ctx).Err(err).Msg("Failed to build prompt for pending queue item")
			oc.notifyMatrixSendFailure(ctx, item.pending.Portal, item.pending.Event, err)
			oc.removePendingAckReactions(oc.backgroundContext(ctx), item.pending.Portal, item.pending)
			oc.releaseRoom(roomID)
			oc.processPendingQueue(oc.backgroundContext(ctx), roomID)
			return
		}

		oc.dispatchPromptRun(ctx, roomID, item, promptContext, true)
	}()
}
