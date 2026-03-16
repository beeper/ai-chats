package ai

import (
	"context"
	"errors"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func (oc *AIClient) dispatchInternalMessage(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	body string,
	source string,
	excludeFromHistory bool,
) (id.EventID, bool, error) {
	if oc == nil || portal == nil || portal.MXID == "" {
		return "", false, errors.New("missing portal context")
	}
	if meta == nil {
		meta = portalMeta(portal)
		if meta == nil {
			return "", false, errors.New("missing portal metadata")
		}
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "", false, errors.New("message body is required")
	}

	prefix := "internal"
	if src := strings.TrimSpace(source); src != "" {
		prefix = src
	}
	eventID := agentremote.NewEventID(prefix)

	inboundCtx := oc.resolvePromptInboundContext(ctx, portal, trimmed, eventID)
	promptCtx := withInboundContext(ctx, inboundCtx)
	promptContext, err := oc.buildContextWithLinkContext(promptCtx, portal, meta, trimmed, nil, eventID)
	if err != nil {
		return eventID, false, err
	}

	userMessage := &database.Message{
		ID:       agentremote.MatrixMessageID(eventID),
		MXID:     eventID,
		Room:     portal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: agentremote.BaseMessageMetadata{Role: "user", Body: trimmed, ExcludeFromHistory: excludeFromHistory},
		},
		Timestamp: time.Now(),
	}
	setCanonicalTurnDataFromPromptMessages(userMessage.Metadata.(*MessageMetadata), promptTail(promptContext, 1))
	ensureCanonicalUserMessage(userMessage)
	if _, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userMessage.SenderID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure user ghost before saving internal message")
	}
	if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, userMessage); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to save internal message to database")
	}

	isGroup := oc.isGroupChat(ctx, portal)
	pending := pendingMessage{
		Portal:         portal,
		Meta:           meta,
		InboundContext: &inboundCtx,
		Type:           pendingTypeText,
		MessageBody:    trimmed,
		Typing: &TypingContext{
			IsGroup:      isGroup,
			WasMentioned: true,
		},
	}
	queueItem := pendingQueueItem{
		pending:     pending,
		messageID:   string(eventID),
		summaryLine: trimmed,
		enqueuedAt:  time.Now().UnixMilli(),
	}
	queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(ctx, portal, meta, "", airuntime.QueueInlineOptions{})

	if oc.acquireRoom(portal.MXID) {
		metaSnapshot := clonePortalMetadata(meta)
		runCtx := oc.attachRoomRun(withInboundContext(oc.backgroundContext(ctx), inboundCtx), portal.MXID)
		runCtx = WithTypingContext(runCtx, pending.Typing)
		go func(metaSnapshot *PortalMetadata) {
			defer func() {
				oc.releaseRoom(portal.MXID)
				oc.processPendingQueue(oc.backgroundContext(ctx), portal.MXID)
			}()
			oc.dispatchCompletionInternal(runCtx, nil, portal, metaSnapshot, promptContext)
		}(metaSnapshot)
		oc.notifySessionMutation(ctx, portal, meta, false)
		return eventID, false, nil
	}

	behavior := airuntime.ResolveQueueBehavior(queueSettings.Mode)
	shouldSteer := behavior.Steer
	queueDecision := airuntime.DecideQueueAction(queueSettings.Mode, oc.roomHasActiveRun(portal.MXID), false)
	if queueDecision.Action == airuntime.QueueActionInterruptAndRun {
		oc.cancelRoomRun(portal.MXID)
		oc.clearPendingQueue(portal.MXID)
	}
	if shouldSteer && pending.Type == pendingTypeText {
		queueItem.prompt = pending.MessageBody
		if oc.enqueueSteerQueue(portal.MXID, queueItem) {
			if !behavior.BacklogAfter {
				return eventID, true, nil
			}
		}
	}
	if behavior.BacklogAfter {
		queueItem.backlogAfter = true
	}
	oc.queuePendingMessage(portal.MXID, queueItem, queueSettings)
	oc.notifySessionMutation(ctx, portal, meta, false)
	return eventID, true, nil
}
