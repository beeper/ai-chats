package ai

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/sdk"
)

func (oc *AIClient) roomHasActiveRun(roomID id.RoomID) bool {
	return oc.getRoomRun(roomID) != nil
}

func (oc *AIClient) acquireRoom(roomID id.RoomID) bool {
	if oc == nil || roomID == "" {
		return false
	}
	oc.activeRoomRunsMu.Lock()
	defer oc.activeRoomRunsMu.Unlock()
	if oc.activeRoomRuns == nil {
		oc.activeRoomRuns = make(map[id.RoomID]*roomRunState)
	}
	if oc.activeRoomRuns[roomID] != nil {
		return false
	}
	oc.activeRoomRuns[roomID] = &roomRunState{}
	return true
}

// releaseRoom releases a room after processing is complete.
func (oc *AIClient) releaseRoom(roomID id.RoomID) {
	oc.clearRoomRun(roomID)
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

func (oc *AIClient) sendPendingMessageStatus(ctx context.Context, portal *bridgev2.Portal, events []*event.Event, message string) {
	if portal == nil || portal.Bridge == nil {
		return
	}
	for _, evt := range events {
		if evt == nil {
			continue
		}
		info := sdk.StatusEventInfoFromPortalEvent(portal, evt)
		if info == nil {
			continue
		}
		status := bridgev2.MessageStatus{
			Status:    event.MessageStatusPending,
			Message:   message,
			IsCertain: true,
		}
		portal.Bridge.Matrix.SendMessageStatus(ctx, &status, info)
	}
}

func (oc *AIClient) buildPromptContextForPendingMessage(
	ctx context.Context,
	pending pendingMessage,
	promptText string,
) (PromptContext, error) {
	if pending.InboundContext != nil {
		ctx = withInboundContext(ctx, *pending.InboundContext)
	}
	metaSnapshot := clonePortalMetadata(pending.Meta)
	eventID := id.EventID("")
	if pending.Event != nil {
		eventID = pending.Event.ID
	}
	switch pending.Type {
	case pendingTypeText:
		if promptText == "" {
			promptText = pending.MessageBody
		}
		return oc.buildPromptContextForTurn(ctx, pending.Portal, metaSnapshot, promptText, eventID, currentTurnPromptOptions{
			currentTurnTextOptions: currentTurnTextOptions{
				rawEventContent:  pending.RawEventContent,
				includeLinkScope: true,
			},
		})
	case pendingTypeImage, pendingTypePDF, pendingTypeAudio, pendingTypeVideo:
		return oc.buildPromptContextForTurn(ctx, pending.Portal, metaSnapshot, pending.MessageBody, eventID, currentTurnPromptOptions{
			currentTurnTextOptions: currentTurnTextOptions{includeLinkScope: true},
			attachment: &turnAttachmentOptions{
				mediaURL:      pending.MediaURL,
				mimeType:      pending.MimeType,
				encryptedFile: pending.EncryptedFile,
				mediaType:     pending.Type,
			},
		})
	case pendingTypeRegenerate:
		return oc.buildContextForRegenerate(ctx, pending.Portal, metaSnapshot, pending.MessageBody)
	case pendingTypeEditRegenerate:
		return oc.buildContextUpToMessage(ctx, pending.Portal, metaSnapshot, pending.TargetMsgID, pending.MessageBody)
	default:
		return PromptContext{}, fmt.Errorf("unknown pending message type: %s", pending.Type)
	}
}

func (oc *AIClient) dispatchPromptRun(
	ctx context.Context,
	roomID id.RoomID,
	item pendingQueueItem,
	promptContext PromptContext,
) {
	runCtx := oc.attachRoomRun(oc.backgroundContext(ctx), roomID)
	if run := oc.getRoomRun(roomID); run != nil {
		run.mu.Lock()
		oc.registerRoomRunPendingItemLocked(run, item)
		run.mu.Unlock()
	}
	if item.pending.InboundContext != nil {
		runCtx = withInboundContext(runCtx, *item.pending.InboundContext)
	}
	if item.pending.Typing != nil {
		runCtx = WithTypingContext(runCtx, item.pending.Typing)
	}
	metaSnapshot := clonePortalMetadata(item.pending.Meta)
	oc.launchAgentLoopRun(runCtx, item.pending.Event, item.pending.Portal, metaSnapshot, promptContext, func() {
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
			if oc.enqueuePendingItem(roomID, followup, queueSettings) {
				oc.startQueueTyping(oc.backgroundContext(context.Background()), followup.pending.Portal, followup.pending.Meta, followup.pending.Typing)
			}
		}
		oc.releaseRoom(roomID)
		oc.processPendingQueue(oc.backgroundContext(ctx), roomID)
	})
}

// dispatchOrQueueCore contains shared dispatch/steer/queue logic.
func (oc *AIClient) dispatchOrQueueCore(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	queueItem pendingQueueItem,
	queueSettings airuntime.QueueSettings,
	promptContext PromptContext,
) error {
	roomID := portal.MXID
	behavior := airuntime.ResolveQueueBehavior(queueSettings.Mode)
	shouldSteer := behavior.Steer
	shouldFollowup := behavior.Followup
	roomBusy := oc.roomHasActiveRun(roomID) || oc.roomHasPendingQueueWork(roomID)
	if queueSettings.Mode == airuntime.QueueModeInterrupt && roomBusy {
		oc.cancelRoomRun(roomID)
		oc.clearPendingQueue(ctx, roomID)
		roomBusy = false
	}

	directRun := !roomBusy && oc.acquireRoom(roomID)
	if directRun {
		oc.stopQueueTyping(roomID)
		queuedItem := queueItem
		queuedItem.pending.Portal = portal
		queuedItem.pending.Meta = meta
		queuedItem.pending.Event = evt
		oc.dispatchPromptRun(ctx, roomID, queuedItem, promptContext)
		return nil
	}

	steered := false
	if !directRun && shouldSteer && queueItem.pending.Type == pendingTypeText {
		queueItem.prompt = queueItem.pending.MessageBody
		steered = oc.enqueueSteerQueue(roomID, queueItem)
	}

	queueNeeded := !directRun && (!steered || shouldFollowup)
	if queueNeeded {
		if behavior.BacklogAfter {
			queueItem.backlogAfter = true
		}
		enqueued := oc.enqueuePendingItem(roomID, queueItem, queueSettings)
		if !enqueued {
			err := fmt.Errorf("couldn't queue the message")
			return bridgev2.WrapErrorInStatus(err).
				WithStatus(event.MessageStatusRetriable).
				WithErrorReason(event.MessageStatusGenericError).
				WithMessage("Couldn't queue the message. Try again.").
				WithIsCertain(true).
				WithSendNotice(false)
		}
		if !queueItem.pending.PendingSent {
			statusEvents := queueStatusEvents(evt, queueItem.pending.StatusEvents)
			oc.sendPendingMessageStatus(ctx, portal, statusEvents, "Queued — waiting for current turn to finish...")
		}
		oc.startQueueTyping(oc.backgroundContext(context.Background()), queueItem.pending.Portal, queueItem.pending.Meta, queueItem.pending.Typing)
	}
	return nil
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

		candidate := oc.takePendingQueueDispatchCandidate(roomID, false)
		if candidate == nil || len(candidate.items) == 0 {
			oc.releaseRoom(roomID)
			return
		}

		item, prompt, ok := preparePendingQueueDispatchCandidate(candidate)
		if !ok {
			oc.releaseRoom(roomID)
			return
		}

		promptContext, err := oc.buildPromptContextForPendingMessage(ctx, item.pending, prompt)
		if err != nil {
			oc.loggerForContext(ctx).Err(err).Msg("Failed to build prompt for pending queue item")
			oc.notifyMatrixSendFailure(ctx, item.pending.Portal, item.pending.Event, err)
			oc.removePendingAckReactions(oc.backgroundContext(ctx), item.pending.Portal, item.pending)
			oc.releaseRoom(roomID)
			oc.processPendingQueue(oc.backgroundContext(ctx), roomID)
			return
		}

		oc.dispatchPromptRun(ctx, roomID, item, promptContext)
	}()
}
