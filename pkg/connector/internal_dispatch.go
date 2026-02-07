package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
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
	trace := traceEnabled(meta)
	traceFull := traceFull(meta)
	if trace {
		oc.loggerForContext(ctx).Debug().
			Stringer("portal", portal.PortalKey).
			Str("source", strings.TrimSpace(source)).
			Msg("Dispatching internal message")
	}
	if meta.IsOpenCodeRoom {
		if oc.opencodeBridge == nil {
			return "", false, errors.New("OpenCode integration is not available")
		}
		if trace {
			oc.loggerForContext(ctx).Debug().Stringer("portal", portal.PortalKey).Msg("Routing internal message to OpenCode")
		}
		return oc.opencodeBridge.DispatchInternalMessage(ctx, portal, oc.PortalMeta(portal), body)
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "", false, errors.New("message body is required")
	}
	if traceFull {
		oc.loggerForContext(ctx).Debug().Stringer("portal", portal.PortalKey).Str("body", trimmed).Msg("Internal message body")
	}

	prefix := "internal"
	if src := strings.TrimSpace(source); src != "" {
		prefix = src
	}
	eventID := id.EventID(fmt.Sprintf("$%s-%s", prefix, uuid.NewString()))

	promptMessages, err := oc.buildPrompt(ctx, portal, meta, trimmed, eventID)
	if err != nil {
		return eventID, false, err
	}

	userMessage := &database.Message{
		ID:       networkid.MessageID(fmt.Sprintf("mx:%s", eventID)),
		MXID:     eventID,
		Room:     portal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: &MessageMetadata{
			Role:               "user",
			Body:               trimmed,
			ExcludeFromHistory: excludeFromHistory,
		},
		Timestamp: time.Now(),
	}
	if _, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userMessage.SenderID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure user ghost before saving internal message")
	}
	if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, userMessage); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to save internal message to database")
	}

	isGroup := oc.isGroupChat(ctx, portal)
	pending := pendingMessage{
		Portal:      portal,
		Meta:        meta,
		Type:        pendingTypeText,
		MessageBody: trimmed,
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
	queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(ctx, portal, meta, "", QueueInlineOptions{})

	if oc.acquireRoom(portal.MXID) {
		metaSnapshot := clonePortalMetadata(meta)
		runCtx := oc.attachRoomRun(oc.backgroundContext(ctx), portal.MXID)
		runCtx = WithTypingContext(runCtx, pending.Typing)
		go func(metaSnapshot *PortalMetadata) {
			defer func() {
				oc.releaseRoom(portal.MXID)
				oc.processPendingQueue(oc.backgroundContext(ctx), portal.MXID)
			}()
			oc.dispatchCompletionInternal(runCtx, nil, portal, metaSnapshot, promptMessages)
		}(metaSnapshot)
		oc.notifySessionMemoryChange(ctx, portal, meta, false)
		return eventID, false, nil
	}

	shouldSteer := queueSettings.Mode == QueueModeSteer || queueSettings.Mode == QueueModeSteerBacklog
	if queueSettings.Mode == QueueModeInterrupt {
		oc.cancelRoomRun(portal.MXID)
		oc.clearPendingQueue(portal.MXID)
	}
	if shouldSteer && pending.Type == pendingTypeText {
		queueItem.prompt = pending.MessageBody
		if pending.Event != nil {
			queueItem.prompt = appendMessageIDHint(queueItem.prompt, pending.Event.ID)
		}
		if oc.enqueueSteerQueue(portal.MXID, queueItem) {
			if queueSettings.Mode != QueueModeSteerBacklog {
				if trace {
					oc.loggerForContext(ctx).Debug().Stringer("portal", portal.PortalKey).Msg("Steered internal message into active run")
				}
				return eventID, true, nil
			}
		}
	}
	if queueSettings.Mode == QueueModeSteerBacklog {
		queueItem.backlogAfter = true
	}
	if trace {
		oc.loggerForContext(ctx).Debug().Stringer("portal", portal.PortalKey).Msg("Queued internal message")
	}
	oc.queuePendingMessage(portal.MXID, queueItem, queueSettings)
	oc.notifySessionMemoryChange(ctx, portal, meta, false)
	return eventID, true, nil
}
