package connector

import (
	"context"
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
		return "", false, fmt.Errorf("missing portal context")
	}
	if meta == nil {
		meta = portalMeta(portal)
		if meta == nil {
			return "", false, fmt.Errorf("missing portal metadata")
		}
	}
	if meta.IsOpenCodeRoom {
		if oc.opencodeBridge == nil {
			return "", false, fmt.Errorf("OpenCode integration is not available")
		}
		return oc.opencodeBridge.DispatchInternalMessage(ctx, portal, oc.PortalMeta(portal), body)
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "", false, fmt.Errorf("message body is required")
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
		oc.log.Warn().Err(err).Msg("Failed to ensure user ghost before saving internal message")
	}
	if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, userMessage); err != nil {
		oc.log.Warn().Err(err).Msg("Failed to save internal message to database")
	}

	pending := pendingMessage{
		Portal:      portal,
		Meta:        meta,
		Type:        pendingTypeText,
		MessageBody: trimmed,
	}

	if oc.acquireRoom(portal.MXID) {
		metaSnapshot := clonePortalMetadata(meta)
		runCtx := oc.backgroundContext(ctx)
		go func(metaSnapshot *PortalMetadata) {
			defer func() {
				oc.releaseRoom(portal.MXID)
				oc.processNextPending(oc.backgroundContext(ctx), portal.MXID)
			}()
			oc.dispatchCompletionInternal(runCtx, nil, portal, metaSnapshot, promptMessages)
		}(metaSnapshot)
		oc.notifySessionMemoryChange(ctx, portal, meta, false)
		return eventID, false, nil
	}

	oc.queuePendingMessage(portal.MXID, pending)
	oc.notifySessionMemoryChange(ctx, portal, meta, false)
	return eventID, true, nil
}
