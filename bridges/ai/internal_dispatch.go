package ai

import (
	"context"
	"errors"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/sdk"
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
	eventID := sdk.NewEventID(prefix)

	inboundCtx := oc.resolvePromptInboundContext(ctx, portal, trimmed, eventID)
	promptCtx := withInboundContext(ctx, inboundCtx)
	promptContext, err := oc.buildCurrentTurnWithLinks(promptCtx, portal, meta, trimmed, nil, eventID)
	if err != nil {
		return eventID, false, err
	}

	if err := oc.persistAIInternalPromptTurn(ctx, portal, eventID, promptContext, excludeFromHistory, prefix, time.Now()); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist internal prompt message")
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
	queueSettings := oc.resolveQueueSettingsForPortal(ctx, portal, meta, "", airuntime.QueueInlineOptions{})
	_, isPending := oc.dispatchOrQueue(promptCtx, nil, portal, meta, nil, queueItem, queueSettings, promptContext)
	oc.notifySessionMutation(ctx, portal, meta, false)
	return eventID, isPending, nil
}
