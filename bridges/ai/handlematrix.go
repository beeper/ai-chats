package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

// HandleMatrixMessage processes incoming Matrix messages and dispatches them to the AI
func (oc *AIClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if msg.Content == nil {
		return nil, errors.New("missing message content")
	}

	portal := msg.Portal
	if portal == nil {
		return nil, errors.New("portal is nil")
	}
	var err error
	portal, err = resolvePortalForAIDB(ctx, oc, portal)
	if err != nil {
		return nil, fmt.Errorf("failed to canonicalize portal for inbound message: %w", err)
	}
	msg.Portal = portal
	meta := portalMeta(portal)
	if msg.Event == nil {
		return nil, errors.New("missing message event")
	}
	logCtx := oc.loggerForContext(ctx).With().
		Stringer("event_id", msg.Event.ID).
		Stringer("sender", msg.Event.Sender).
		Stringer("portal", portal.PortalKey).
		Logger()
	ctx = logCtx.WithContext(ctx)

	// Check deduplication - skip if we've already processed this event
	if oc.inboundDedupeCache != nil {
		dedupeKey := oc.buildDedupeKey(portal.MXID, msg.Event.ID)
		if oc.inboundDedupeCache.Check(dedupeKey) {
			logCtx.Debug().Msg("Skipping duplicate message")
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
	}

	if sdk.IsMatrixBotUser(ctx, oc.UserLogin.Bridge, msg.Event.Sender) {
		logCtx.Debug().Msg("Ignoring bot message")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	// Normalize sticker events to image handling
	msgType := msg.Content.MsgType
	if msg.Event != nil && msg.Event.Type == event.EventSticker {
		msgType = event.MsgImage
	}
	if msgType == event.MessageType(event.EventSticker.Type) {
		msgType = event.MsgImage
	}
	if msgType == event.MsgVideo && msg.Content.Info != nil && msg.Content.Info.MauGIF {
		if mimeType := stringutil.NormalizeMimeType(msg.Content.Info.MimeType); strings.HasPrefix(mimeType, "image/") {
			msgType = event.MsgImage
		}
	}

	// Handle media messages based on type (media is never debounced)
	switch msgType {
	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		logCtx.Debug().Str("media_type", string(msgType)).Msg("Handling media message")
		// Flush any pending debounced messages for this room+sender before processing media
		if oc.inboundDebouncer != nil {
			debounceKey := BuildDebounceKey(portal.MXID, msg.Event.Sender)
			oc.inboundDebouncer.flush(debounceKey)
		}
		pendingSent := false
		return oc.handleMediaMessage(ctx, msg, portal, meta, msgType, pendingSent)
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		// Continue to text handling below
	default:
		logCtx.Debug().Str("msg_type", string(msgType)).Msg("Unsupported message type")
		return nil, sdk.UnsupportedMessageStatus(fmt.Errorf("%s messages are not supported", msgType))
	}
	if msg.Content.RelatesTo != nil && msg.Content.RelatesTo.GetReplaceID() != "" {
		logCtx.Debug().Msg("Ignoring edit event in HandleMatrixMessage")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	rawBody := strings.TrimSpace(msg.Content.Body)
	if msg.Content.MsgType == event.MsgLocation && strings.TrimSpace(msg.Content.GeoURI) != "" {
		rawMap := msg.Event.Content.Raw
		if loc := resolveMatrixLocation(rawMap); loc != nil && strings.TrimSpace(loc.Text) != "" {
			rawBody = loc.Text
		}
	}
	rawBodyOriginal := rawBody
	commandAuthorized := oc.isCommandAuthorizedSender(msg.Event.Sender)

	isGroup := oc.isGroupChat(ctx, portal)
	roomName := ""
	if isGroup {
		roomName = oc.matrixRoomDisplayName(ctx, portal)
	}
	senderName := oc.matrixDisplayName(ctx, portal.MXID, msg.Event.Sender)
	logCtx.Debug().
		Bool("is_group", isGroup).
		Bool("command_authorized", commandAuthorized).
		Int("raw_len", len(rawBodyOriginal)).
		Msg("Inbound message metadata resolved")

	mc := oc.resolveMentionContext(ctx, portal, meta, msg.Event, msg.Content.Mentions, rawBody)

	var cfg *Config
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	queueSettings := resolveQueueSettings(queueResolveParams{cfg: cfg, channel: "matrix", inlineOpts: airuntime.QueueInlineOptions{}})

	commandBody := rawBody
	if isGroup {
		commandBody = stripMentionPatterns(commandBody, mc.MentionRegexes)
	}
	if !commandAuthorized && airuntime.IsAbortTriggerText(commandBody) {
		logCtx.Debug().Msg("Ignoring abort trigger from unauthorized sender")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	if commandAuthorized && airuntime.IsAbortTriggerText(commandBody) {
		replyCtx := extractInboundReplyContext(msg.Event)
		result := oc.handleUserStop(ctx, userStopRequest{
			Portal:             portal,
			Meta:               meta,
			ReplyTo:            replyCtx.ReplyTo,
			RequestedByEventID: msg.Event.ID,
			RequestedVia:       "text-trigger",
		})
		oc.sendSystemNotice(ctx, portal, formatAbortNotice(result))
		logCtx.Debug().Msg("Abort trigger handled")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	runMeta := meta
	runCtx := ctx

	if rawBody == "" {
		return nil, sdk.UnsupportedMessageStatus(errors.New("empty messages are not supported"))
	}

	wasMentioned := mc.WasMentioned
	groupActivation := oc.resolveGroupActivation(meta)
	requireMention := isGroup && groupActivation != "always"
	canDetectMention := len(mc.MentionRegexes) > 0 || mc.HasExplicit
	shouldBypassMention := groupActivation == "always"
	if isGroup && requireMention && !wasMentioned && !shouldBypassMention {
		logCtx.Debug().
			Bool("require_mention", requireMention).
			Bool("was_mentioned", wasMentioned).
			Str("activation", groupActivation).
			Msg("Ignoring group message without mention")
		historyLimit := oc.resolveGroupHistoryLimit()
		if historyLimit > 0 {
			historyBody := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, rawBodyOriginal, senderName, roomName, isGroup)
			oc.recordPendingGroupHistory(portal.MXID, historyBody, historyLimit)
		}
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	pendingSent := false

	// Ack reaction.
	ackReaction := strings.TrimSpace(meta.AckReactionEmoji)
	if ackReaction == "" && oc.connector != nil && oc.connector.Config.Messages != nil {
		ackReaction = strings.TrimSpace(oc.connector.Config.Messages.AckReaction)
	}
	ackScope := AckScopeGroupMention
	if oc.connector != nil && oc.connector.Config.Messages != nil {
		ackScope = normalizeAckScope(oc.connector.Config.Messages.AckReactionScope)
	}
	removeAckAfter := meta.AckReactionRemoveAfter
	if !removeAckAfter && oc.connector != nil && oc.connector.Config.Messages != nil && oc.connector.Config.Messages.RemoveAckAfter {
		removeAckAfter = true
	}
	meta.AckReactionRemoveAfter = removeAckAfter

	var ackReactionEventID id.EventID
	if ackReaction != "" && shouldAckReaction(AckReactionGateParams{
		Scope:              ackScope,
		IsDirect:           !isGroup,
		IsGroup:            isGroup,
		IsMentionableGroup: isGroup,
		RequireMention:     requireMention,
		CanDetectMention:   canDetectMention,
		EffectiveMention:   wasMentioned || shouldBypassMention,
		ShouldBypass:       shouldBypassMention,
	}) {
		ackReactionEventID = oc.sendAckReaction(ctx, portal, msg.Event.ID, ackReaction)
	}
	if ackReactionEventID != "" && removeAckAfter {
		oc.storeAckReaction(ctx, portal, msg.Event.ID, ackReaction)
	}
	body := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, rawBody, senderName, roomName, isGroup)
	inboundCtx := oc.buildMatrixInboundContext(portal, msg.Event, rawBody, senderName, roomName, isGroup)
	runCtx = withInboundContext(runCtx, inboundCtx)
	if isGroup && requireMention {
		body = oc.buildGroupHistoryContext(portal.MXID, body, oc.resolveGroupHistoryLimit())
	}

	debounceDelay := meta.DebounceMs
	if debounceDelay == 0 {
		debounceDelay = oc.resolveInboundDebounceMs("matrix")
	}
	shouldDebounce := oc.inboundDebouncer != nil && ShouldDebounce(msg.Event, rawBody) && debounceDelay > 0
	debounceKey := ""
	if oc.inboundDebouncer != nil {
		debounceKey = BuildDebounceKey(portal.MXID, msg.Event.Sender)
	}
	if shouldDebounce {
		logCtx.Debug().Int("debounce_ms", debounceDelay).Msg("Debouncing inbound message")
		userMessage := &database.Message{
			ID:       sdk.MatrixMessageID(msg.Event.ID),
			MXID:     msg.Event.ID,
			Room:     portal.PortalKey,
			SenderID: humanUserID(oc.UserLogin.ID),
			Metadata: &MessageMetadata{
				BaseMessageMetadata: sdk.BaseMessageMetadata{Role: "user", Body: body},
			},
			Timestamp: sdk.MatrixEventTimestamp(msg.Event),
		}
		entry := DebounceEntry{
			Event:        msg.Event,
			Portal:       portal,
			Meta:         runMeta,
			InboundCtx:   inboundCtx,
			RawBody:      rawBody,
			SenderName:   senderName,
			RoomName:     roomName,
			IsGroup:      isGroup,
			WasMentioned: wasMentioned,
			AckEventID:   ackReactionEventID,
			DBMessage:    userMessage,
		}
		if !pendingSent {
			oc.sendPendingMessageStatus(ctx, portal, []*event.Event{msg.Event}, "Combining messages...")
			entry.PendingSent = true
		}
		oc.inboundDebouncer.EnqueueWithDelay(debounceKey, entry, true, debounceDelay)
		return &bridgev2.MatrixMessageResponse{DB: userMessage, Pending: true}, nil
	}
	if debounceKey != "" {
		// Flush any pending debounced messages for this room+sender before immediate processing
		oc.inboundDebouncer.flush(debounceKey)
	}

	// Not debouncing - process immediately
	// Get raw event content for link previews
	var rawEventContent map[string]any
	if msg.Event != nil && msg.Event.Content.Raw != nil {
		rawEventContent = clonePendingRawMap(msg.Event.Content.Raw)
	}
	pendingEvent := snapshotPendingEvent(msg.Event)

	eventID := id.EventID("")
	if msg.Event != nil {
		eventID = msg.Event.ID
	}

	pending := pendingMessage{
		Event:           pendingEvent,
		Portal:          portal,
		Meta:            runMeta,
		InboundContext:  &inboundCtx,
		Type:            pendingTypeText,
		MessageBody:     body,
		RawEventContent: rawEventContent,
		AckEventIDs:     []id.EventID{msg.Event.ID},
		PendingSent:     pendingSent,
		Typing: &TypingContext{
			IsGroup:      isGroup,
			WasMentioned: wasMentioned,
		},
	}
	promptContext, err := oc.buildPromptContextForPendingMessage(runCtx, pending, body)
	if err != nil {
		return nil, sdk.MessageSendStatusError(err, "Couldn't prepare the message. Try again.", "", messageStatusForError, messageStatusReasonForError)
	}
	logCtx.Debug().Int("prompt_messages", len(promptContext.Messages)).Msg("Built prompt for inbound message")
	userMessage := &database.Message{
		ID:       sdk.MatrixMessageID(eventID),
		MXID:     eventID,
		Room:     portal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: attachPromptTurnData(&MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{Role: "user", Body: body},
		}, promptContext),
		Timestamp: sdk.MatrixEventTimestamp(msg.Event),
	}
	queueItem := pendingQueueItem{
		pending: pending,
		acceptedMessages: []*database.Message{
			userMessage,
		},
		messageID:   string(eventID),
		summaryLine: rawBodyOriginal,
		enqueuedAt:  time.Now().UnixMilli(),
	}
	if err = oc.dispatchOrQueueCore(runCtx, pendingEvent, portal, runMeta, queueItem, queueSettings, promptContext); err != nil {
		return nil, err
	}
	return &bridgev2.MatrixMessageResponse{DB: userMessage, Pending: true}, nil
}

// HandleMatrixTyping currently ignores local typing updates.
func (oc *AIClient) HandleMatrixTyping(ctx context.Context, typing *bridgev2.MatrixTyping) error {
	return nil
}
