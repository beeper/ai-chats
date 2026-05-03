package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
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

// HandleMatrixEdit handles edits to previously sent messages
func (oc *AIClient) HandleMatrixEdit(ctx context.Context, edit *bridgev2.MatrixEdit) error {
	portal := edit.Portal
	if portal == nil {
		return errors.New("portal is nil")
	}
	var err error
	portal, err = resolvePortalForAIDB(ctx, oc, portal)
	if err != nil {
		return fmt.Errorf("failed to canonicalize portal for edit: %w", err)
	}
	edit.Portal = portal
	meta := portalMeta(portal)
	if meta != nil && meta.ResolvedTarget != nil && meta.ResolvedTarget.Kind == ResolvedTargetModel {
		return bridgev2.ErrEditsNotSupportedInPortal
	}
	if edit.Content == nil || edit.EditTarget == nil {
		return errors.New("invalid edit: missing content or target")
	}

	// Get the new message body
	newBody := strings.TrimSpace(edit.Content.Body)
	if newBody == "" {
		return errors.New("empty edit body")
	}

	// Update the message metadata with the new content
	msgMeta := messageMeta(edit.EditTarget)
	if msgMeta == nil {
		msgMeta = &MessageMetadata{}
		edit.EditTarget.Metadata = msgMeta
	}
	transcriptMsg, err := oc.loadAIConversationMessage(ctx, portal, edit.EditTarget.ID, edit.EditTarget.MXID)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load edited conversation turn")
	}
	if transcriptMsg == nil {
		transcriptMsg = cloneMessageForAIHistory(edit.EditTarget)
	}
	transcriptMeta, ok := transcriptMsg.Metadata.(*MessageMetadata)
	if !ok || transcriptMeta == nil {
		transcriptMeta = cloneMessageMetadata(msgMeta)
		if transcriptMeta == nil {
			transcriptMeta = &MessageMetadata{}
		}
		transcriptMsg.Metadata = transcriptMeta
	}
	transcriptMeta.Body = newBody
	role := strings.TrimSpace(transcriptMeta.Role)
	if role == "" {
		role = strings.TrimSpace(msgMeta.Role)
	}
	if role == "user" {
		if _, turnData, ok := buildUserPromptTurn([]PromptBlock{{
			Type: PromptBlockText,
			Text: newBody,
		}}); ok {
			transcriptMeta.CanonicalTurnData = turnData.ToMap()
		} else {
			transcriptMeta.CanonicalTurnData = nil
		}
	} else {
		transcriptMeta.CanonicalTurnData = nil
	}
	if err := oc.persistAIConversationMessage(ctx, portal, transcriptMsg); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist edited conversation turn")
		return err
	}
	if edit.EditTarget != nil {
		edit.EditTarget.Metadata = cloneMessageMetadata(transcriptMeta)
	}
	// Only regenerate if this was a user message
	if role != "user" {
		// Just update the content, don't regenerate
		return nil
	}

	oc.loggerForContext(ctx).Info().
		Str("message_id", string(edit.EditTarget.ID)).
		Int("new_body_len", len(newBody)).
		Msg("User edited message, regenerating response")

	// Find the assistant response that came after this message
	// We'll delete it and regenerate
	err = oc.regenerateFromEdit(ctx, edit.Event, portal, meta, edit.EditTarget, newBody)
	if err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to regenerate response after edit")
		oc.sendSystemNotice(ctx, portal, fmt.Sprintf("Couldn't regenerate the response: %v", err))
	}

	return nil
}

// regenerateFromEdit regenerates the AI response based on an edited user message
func (oc *AIClient) regenerateFromEdit(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	editedMessage *database.Message,
	newBody string,
) error {
	// Get messages in the portal to find the assistant response after the edited message
	messages, err := oc.getAIHistoryMessages(ctx, portal, 50)
	if err != nil {
		return fmt.Errorf("failed to get messages: %w", err)
	}

	// Find the assistant response that came after the edited message
	// Messages come newest-first from GetLastNInPortal, so lower indices are newer
	var assistantResponse *database.Message

	// First find the index of the edited message
	editedIdx := -1
	for i, msg := range messages {
		if msg.ID == editedMessage.ID {
			editedIdx = i
			break
		}
	}

	if editedIdx > 0 {
		// Search toward newer messages (lower indices) for assistant response
		for i := editedIdx - 1; i >= 0; i-- {
			msgMeta := messageMeta(messages[i])
			if msgMeta != nil && msgMeta.Role == "assistant" {
				assistantResponse = messages[i]
				break
			}
		}
	}

	pending := pendingMessage{
		Event:       snapshotPendingEvent(evt),
		Portal:      portal,
		Meta:        meta,
		Type:        pendingTypeEditRegenerate,
		MessageBody: newBody,
		TargetMsgID: editedMessage.ID,
		Typing: &TypingContext{
			IsGroup:      oc.isGroupChat(ctx, portal),
			WasMentioned: true,
		},
	}
	// Build the prompt with the edited message included.
	promptContext, err := oc.buildPromptContextForPendingMessage(ctx, pending, "")
	if err != nil {
		return fmt.Errorf("failed to build prompt: %w", err)
	}

	// If we found an assistant response, we'll redact/edit it
	if assistantResponse != nil {
		// Try to redact the old response
		if assistantResponse.MXID != "" {
			_ = oc.redactEventViaPortal(ctx, portal, assistantResponse.MXID)
		}
	}

	var cfg *Config
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	queueSettings := resolveQueueSettings(queueResolveParams{cfg: cfg, channel: "matrix", inlineOpts: airuntime.QueueInlineOptions{}})
	queueItem := pendingQueueItem{
		pending:     pending,
		messageID:   string(evt.ID),
		summaryLine: newBody,
		enqueuedAt:  time.Now().UnixMilli(),
	}
	return oc.dispatchOrQueueCore(ctx, pending.Event, portal, meta, queueItem, queueSettings, promptContext)
}

// mediaConfig describes how to handle a specific media type
type mediaConfig struct {
	msgType         pendingMessageType
	capabilityCheck func(*ModelCapabilities) bool
	capabilityName  string
	defaultCaption  string
	bodySuffix      string
	defaultMimeType string
}

var mediaConfigs = map[event.MessageType]mediaConfig{
	event.MsgImage: {
		msgType:         pendingTypeImage,
		capabilityCheck: func(c *ModelCapabilities) bool { return c.SupportsVision },
		capabilityName:  "image analysis",
		defaultCaption:  "What's in this image?",
		bodySuffix:      " [image]",
	},
	event.MsgAudio: {
		msgType:         pendingTypeAudio,
		capabilityCheck: func(c *ModelCapabilities) bool { return c.SupportsAudio },
		capabilityName:  "audio input",
		defaultCaption:  "Please transcribe or analyze this audio.",
		bodySuffix:      " [audio]",
		defaultMimeType: "audio/mpeg",
	},
	event.MsgVideo: {
		msgType:         pendingTypeVideo,
		capabilityCheck: func(c *ModelCapabilities) bool { return c.SupportsVideo },
		capabilityName:  "video input",
		defaultCaption:  "Please analyze this video.",
		bodySuffix:      " [video]",
	},
}

// pdfConfig is handled separately due to special OpenRouter capability check
var pdfConfig = mediaConfig{
	msgType:         pendingTypePDF,
	capabilityCheck: func(c *ModelCapabilities) bool { return c.SupportsPDF },
	capabilityName:  "PDF analysis",
	defaultCaption:  "Please analyze this PDF document.",
	bodySuffix:      " [PDF]",
	defaultMimeType: "application/pdf",
}

// handleMediaMessage processes media messages (image, PDF, audio, video)
func (oc *AIClient) handleMediaMessage(
	ctx context.Context,
	msg *bridgev2.MatrixMessage,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	msgType event.MessageType,
	pendingSent bool,
) (*bridgev2.MatrixMessageResponse, error) {
	isGroup := oc.isGroupChat(ctx, portal)
	roomName := ""
	if isGroup {
		roomName = oc.matrixRoomDisplayName(ctx, portal)
	}
	senderName := oc.matrixDisplayName(ctx, portal.MXID, msg.Event.Sender)

	// Get config for this media type
	config, ok := mediaConfigs[msgType]
	isPDF := false

	// Get the media URL
	mediaURL := msg.Content.URL
	if mediaURL == "" && msg.Content.File != nil {
		mediaURL = msg.Content.File.URL
	}
	if mediaURL == "" {
		return nil, sdk.UnsupportedMessageStatus(fmt.Errorf("%s message has no URL", msgType))
	}

	// Get MIME type
	mimeType := ""
	if msg.Content.Info != nil && msg.Content.Info.MimeType != "" {
		mimeType = stringutil.NormalizeMimeType(msg.Content.Info.MimeType)
	}

	// Handle PDF or text files (MsgFile)
	if msgType == event.MsgFile {
		switch {
		case mimeType == "application/pdf":
			config = pdfConfig
			isPDF = true
			ok = true
		case mimeType == "" || mimeType == "application/octet-stream":
			return nil, sdk.UnsupportedMessageStatus(errors.New("text file understanding is not supported"))
		}
	}

	if !ok {
		return nil, sdk.UnsupportedMessageStatus(fmt.Errorf("unsupported media type: %s", msgType))
	}

	if mimeType == "" {
		mimeType = config.defaultMimeType
	}

	eventID := id.EventID("")
	if msg.Event != nil {
		eventID = msg.Event.ID
	}

	// PDFs are normalized into file-context text before prompt assembly, so they
	// do not require native provider file support.
	modelCaps := oc.getModelCapabilitiesForMeta(ctx, meta)
	supportsMedia := config.capabilityCheck(&modelCaps)
	if isPDF {
		supportsMedia = true
	}
	var cfg *Config
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	queueSettings := resolveQueueSettings(queueResolveParams{cfg: cfg, channel: "matrix", inlineOpts: airuntime.QueueInlineOptions{}})

	// Get caption (body is usually the filename or caption)
	rawCaption := strings.TrimSpace(msg.Content.Body)
	hasUserCaption := rawCaption != ""
	if msg.Content.Info != nil && rawCaption == msg.Content.Info.MimeType {
		hasUserCaption = false
	}
	caption := rawCaption
	if !hasUserCaption {
		caption = config.defaultCaption
	}

	mc := oc.resolveMentionContext(ctx, portal, meta, msg.Event, msg.Content.Mentions, rawCaption)
	typingCtx := &TypingContext{IsGroup: isGroup, WasMentioned: mc.WasMentioned}

	// Get encrypted file info if present (for E2EE rooms)
	var encryptedFile *event.EncryptedFileInfo
	if msg.Content.File != nil {
		encryptedFile = msg.Content.File
	}
	pendingEvent := snapshotPendingEvent(msg.Event)

	dispatchTextOnly := func(rawBody string) (*bridgev2.MatrixMessageResponse, error) {
		inboundCtx := oc.buildMatrixInboundContext(portal, msg.Event, rawBody, senderName, roomName, isGroup)
		promptCtx := withInboundContext(ctx, inboundCtx)
		body := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, rawBody, senderName, roomName, isGroup)
		pending := pendingMessage{
			Event:          pendingEvent,
			Portal:         portal,
			Meta:           meta,
			InboundContext: &inboundCtx,
			Type:           pendingTypeText,
			MessageBody:    body,
			PendingSent:    pendingSent,
			Typing:         typingCtx,
		}
		promptContext, err := oc.buildPromptContextForPendingMessage(promptCtx, pending, body)
		if err != nil {
			return nil, sdk.MessageSendStatusError(err, "Couldn't prepare the message. Try again.", "", messageStatusForError, messageStatusReasonForError)
		}
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
			summaryLine: rawBody,
			enqueuedAt:  time.Now().UnixMilli(),
		}
		if err = oc.dispatchOrQueueCore(promptCtx, pendingEvent, portal, meta, queueItem, queueSettings, promptContext); err != nil {
			return nil, err
		}
		return &bridgev2.MatrixMessageResponse{DB: userMessage, Pending: true}, nil
	}

	var understanding *mediaUnderstandingResult
	if capability, ok := mediaCapabilityForMessage(msgType); ok {
		attachments := []mediaAttachment{{
			Index:         0,
			URL:           string(mediaURL),
			MimeType:      mimeType,
			EncryptedFile: encryptedFile,
			FileName:      strings.TrimSpace(msg.Content.FileName),
		}}
		if result, err := oc.applyMediaUnderstandingForAttachments(ctx, portal, meta, capability, attachments, rawCaption, hasUserCaption); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Media understanding failed")
		} else if result != nil {
			understanding = result
			if strings.TrimSpace(result.Body) != "" {
				caption = result.Body
			}
		}
	}

	if msgType == event.MsgAudio || msgType == event.MsgVideo {
		if understanding != nil && strings.TrimSpace(understanding.Body) != "" {
			return dispatchTextOnly(understanding.Body)
		}
		return nil, sdk.UnsupportedMessageStatus(fmt.Errorf(
			"%s messages must be preprocessed into text before generation; configure media understanding or upload a transcript",
			msgType,
		))
	}

	if !supportsMedia {
		if understanding != nil && strings.TrimSpace(understanding.Body) != "" {
			return dispatchTextOnly(understanding.Body)
		}

		// If the model lacks vision but media understanding is configured, analyze image first.
		if msgType == event.MsgImage {
			visionModel, visionFallback := oc.resolveVisionModelForImage(ctx, meta)
			if resp, err := oc.dispatchMediaUnderstandingFallback(
				ctx,
				visionModel,
				visionFallback,
				string(mediaURL),
				mimeType,
				encryptedFile,
				caption,
				hasUserCaption,
				buildMediaUnderstandingPrompt(MediaCapabilityImage),
				oc.analyzeImageWithModel,
				buildMediaUnderstandingMessage("Image", "Description"),
				"Image understanding failed",
				"image understanding produced empty result",
				"Couldn't analyze the image. Try again, or switch to a vision-capable model with !ai model.",
				dispatchTextOnly,
			); resp != nil || err != nil {
				return resp, err
			}
		}

		return nil, sdk.UnsupportedMessageStatus(fmt.Errorf(
			"current model (%s) does not support %s; switch to a capable model using !ai model",
			oc.effectiveModel(meta), config.capabilityName,
		))
	}

	// Build prompt with media
	captionForPrompt := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, caption, senderName, roomName, isGroup)
	captionInboundCtx := oc.buildMatrixInboundContext(portal, msg.Event, caption, senderName, roomName, isGroup)
	promptCtx := withInboundContext(ctx, captionInboundCtx)
	pending := pendingMessage{
		Event:          snapshotPendingEvent(msg.Event),
		Portal:         portal,
		Meta:           meta,
		InboundContext: &captionInboundCtx,
		Type:           config.msgType,
		MessageBody:    captionForPrompt,
		MediaURL:       string(mediaURL),
		MimeType:       mimeType,
		EncryptedFile:  encryptedFile,
		PendingSent:    pendingSent,
		Typing:         typingCtx,
	}
	promptContext, err := oc.buildPromptContextForPendingMessage(promptCtx, pending, "")
	if err != nil {
		return nil, sdk.MessageSendStatusError(err, "Couldn't prepare the media message. Try again.", "", messageStatusForError, messageStatusReasonForError)
	}

	userMeta := &MessageMetadata{
		BaseMessageMetadata: sdk.BaseMessageMetadata{
			Role: "user",
			Body: oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, buildMediaMetadataBody(caption, config.bodySuffix, understanding), senderName, roomName, isGroup),
		},
		MediaURL: string(mediaURL),
		MimeType: mimeType,
	}
	if understanding != nil {
		userMeta.MediaUnderstanding = understanding.Outputs
		userMeta.MediaUnderstandingDecisions = understanding.Decisions
		userMeta.Transcript = understanding.Transcript
	}
	attachPromptTurnData(userMeta, promptContext)

	userMessage := &database.Message{
		ID:        sdk.MatrixMessageID(eventID),
		MXID:      eventID,
		Room:      portal.PortalKey,
		SenderID:  humanUserID(oc.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: sdk.MatrixEventTimestamp(msg.Event),
	}
	queueItem := pendingQueueItem{
		pending: pending,
		acceptedMessages: []*database.Message{
			userMessage,
		},
		messageID:   string(eventID),
		summaryLine: rawCaption,
		enqueuedAt:  time.Now().UnixMilli(),
	}
	if err = oc.dispatchOrQueueCore(promptCtx, pending.Event, portal, meta, queueItem, queueSettings, promptContext); err != nil {
		return nil, err
	}
	return &bridgev2.MatrixMessageResponse{DB: userMessage, Pending: true}, nil
}

func (oc *AIClient) dispatchMediaUnderstandingFallback(
	ctx context.Context,
	model string,
	fallback bool,
	mediaURL string,
	mimeType string,
	encryptedFile *event.EncryptedFileInfo,
	caption string,
	hasUserCaption bool,
	buildPrompt func(string, bool) string,
	analyze func(context.Context, string, string, string, *event.EncryptedFileInfo, string) (string, error),
	buildMessage func(string, bool, string) string,
	failureLog string,
	emptyResult string,
	userError string,
	dispatchTextOnly func(string) (*bridgev2.MatrixMessageResponse, error),
) (*bridgev2.MatrixMessageResponse, error) {
	if !fallback || model == "" {
		return nil, nil
	}
	analysisPrompt := buildPrompt(caption, hasUserCaption)
	description, err := analyze(ctx, model, mediaURL, mimeType, encryptedFile, analysisPrompt)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg(failureLog)
		return nil, sdk.MessageSendStatusError(err, userError, "", messageStatusForError, messageStatusReasonForError)
	}
	if description == "" {
		return nil, sdk.MessageSendStatusError(errors.New(emptyResult), userError, "", messageStatusForError, messageStatusReasonForError)
	}

	combined := buildMessage(caption, hasUserCaption, description)
	if combined == "" {
		return nil, sdk.MessageSendStatusError(errors.New(emptyResult), userError, "", messageStatusForError, messageStatusReasonForError)
	}
	return dispatchTextOnly(combined)
}

func (oc *AIClient) savePortal(ctx context.Context, portal *bridgev2.Portal, action string) error {
	if oc == nil || portal == nil {
		return nil
	}
	var err error
	portal, err = resolvePortalForAIDB(ctx, oc, portal)
	if err != nil {
		return fmt.Errorf("resolve portal for %s: %w", action, err)
	}
	if err := portal.Save(ctx); err != nil {
		return fmt.Errorf("save portal for %s: %w", action, err)
	}
	return nil
}

// savePortalQuiet saves portal and logs errors without failing
func (oc *AIClient) savePortalQuiet(ctx context.Context, portal *bridgev2.Portal, action string) {
	if err := oc.savePortal(ctx, portal, action); err != nil && !errors.Is(err, context.Canceled) {
		oc.loggerForContext(ctx).Warn().Err(err).Str("action", action).Msg("Failed to save portal")
	}
}

// Ack reaction tracking for removal after reply
// Maps room ID -> source message ID -> ack reaction metadata
const (
	ackReactionTTL             = 5 * time.Minute
	ackReactionCleanupInterval = time.Minute
)

type ackReactionEntry struct {
	targetNetworkID networkid.MessageID // Network ID of the target message for reaction removal
	emoji           string              // Emoji used for the reaction
	storedAt        time.Time
}

var (
	ackReactionStore   = make(map[id.RoomID]map[id.EventID]ackReactionEntry)
	ackReactionStoreMu sync.Mutex
	ackCleanupStop     = make(chan struct{})
)

func init() {
	go cleanupAckReactionStore()
}

func cleanupAckReactionStore() {
	ticker := time.NewTicker(ackReactionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-ackReactionTTL)
			ackReactionStoreMu.Lock()
			for roomID, roomReactions := range ackReactionStore {
				for sourceEventID, entry := range roomReactions {
					if entry.storedAt.Before(cutoff) {
						delete(roomReactions, sourceEventID)
					}
				}
				if len(roomReactions) == 0 {
					delete(ackReactionStore, roomID)
				}
			}
			ackReactionStoreMu.Unlock()
		case <-ackCleanupStop:
			return
		}
	}
}

// sendAckReaction sends an acknowledgement reaction to a message via QueueRemoteEvent.
// Returns the event ID of the reaction for potential removal.
func (oc *AIClient) sendAckReaction(ctx context.Context, portal *bridgev2.Portal, targetEventID id.EventID, emoji string) id.EventID {
	if portal == nil || portal.MXID == "" || targetEventID == "" || emoji == "" {
		return ""
	}

	targetPart, err := oc.loadPortalMessagePartByMXID(ctx, portal, targetEventID)
	if err != nil || targetPart == nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("target_event", targetEventID).Msg("Target message not found for ack reaction")
		return ""
	}

	sender := oc.senderForPortal(ctx, portal)
	emojiID := networkid.EmojiID(emoji)
	result := oc.UserLogin.QueueRemoteEvent(sdk.BuildReactionEvent(
		portal.PortalKey,
		sender,
		targetPart.ID,
		emoji,
		emojiID,
		time.Now(),
		0,
		"ai_reaction_target",
		nil,
		nil,
	))
	if !result.Success {
		oc.loggerForContext(ctx).Warn().
			Stringer("target_event", targetEventID).
			Str("emoji", emoji).
			Msg("Failed to send ack reaction")
		return ""
	}

	oc.loggerForContext(ctx).Debug().
		Stringer("target_event", targetEventID).
		Str("emoji", emoji).
		Msg("Sent ack reaction")
	return result.EventID
}

// storeAckReaction stores an ack reaction for later removal.
func (oc *AIClient) storeAckReaction(ctx context.Context, portal *bridgev2.Portal, sourceEventID id.EventID, emoji string) {
	if portal == nil || portal.MXID == "" {
		return
	}
	// Look up the network message ID for the source event
	var targetNetworkID networkid.MessageID
	if part, err := oc.loadPortalMessagePartByMXID(ctx, portal, sourceEventID); err == nil && part != nil {
		targetNetworkID = part.ID
	}

	ackReactionStoreMu.Lock()
	defer ackReactionStoreMu.Unlock()

	if ackReactionStore[portal.MXID] == nil {
		ackReactionStore[portal.MXID] = make(map[id.EventID]ackReactionEntry)
	}
	ackReactionStore[portal.MXID][sourceEventID] = ackReactionEntry{
		targetNetworkID: targetNetworkID,
		emoji:           emoji,
		storedAt:        time.Now(),
	}
}

// removeAckReaction removes a previously sent ack reaction via bridgev2's pipeline.
func (oc *AIClient) removeAckReaction(ctx context.Context, portal *bridgev2.Portal, sourceEventID id.EventID) {
	ackReactionStoreMu.Lock()
	roomReactions := ackReactionStore[portal.MXID]
	if roomReactions == nil {
		ackReactionStoreMu.Unlock()
		return
	}
	entry, ok := roomReactions[sourceEventID]
	if !ok {
		ackReactionStoreMu.Unlock()
		return
	}
	delete(roomReactions, sourceEventID)
	ackReactionStoreMu.Unlock()

	if entry.targetNetworkID == "" || entry.emoji == "" {
		return
	}

	sender := oc.senderForPortal(ctx, portal)
	oc.UserLogin.QueueRemoteEvent(sdk.BuildReactionRemoveEvent(
		portal.PortalKey,
		sender,
		entry.targetNetworkID,
		networkid.EmojiID(entry.emoji),
		time.Now(),
		0,
		"ai_reaction_remove_target",
	))

	oc.loggerForContext(ctx).Debug().
		Stringer("source_event", sourceEventID).
		Str("emoji", entry.emoji).
		Msg("Queued ack reaction removal")
}

// buildPromptForRegenerate builds a prompt for regeneration, excluding the last assistant message
func (oc *AIClient) buildContextForRegenerate(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	latestUserBody string,
) (PromptContext, error) {
	base := PromptContext{
		SystemPrompt: oc.buildConversationSystemPromptText(ctx, portal, meta, false),
	}
	historyMessages, err := oc.replayHistoryMessages(ctx, portal, meta, historyReplayOptions{mode: historyReplayRegen})
	if err != nil {
		return PromptContext{}, err
	}
	base.Messages = append(base.Messages, historyMessages...)
	if userMessage, turnData, ok := buildUserPromptTurn([]PromptBlock{{
		Type: PromptBlockText,
		Text: latestUserBody,
	}}); ok {
		base.Messages = append(base.Messages, userMessage)
		base.CurrentTurnData = turnData
	}
	return base, nil
}
