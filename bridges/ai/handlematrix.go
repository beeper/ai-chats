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

	"github.com/beeper/agentremote"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

func messageSendStatusError(err error, message string, reason event.MessageStatusReason) error {
	return agentremote.MessageSendStatusError(err, message, reason, messageStatusForError, messageStatusReasonForError)
}

// HandleMatrixMessage processes incoming Matrix messages and dispatches them to the AI
func (oc *AIClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if msg.Content == nil {
		return nil, errors.New("missing message content")
	}

	portal := msg.Portal
	if portal == nil {
		return nil, errors.New("portal is nil")
	}
	meta := portalMeta(portal)
	if msg.Event == nil {
		return nil, errors.New("missing message event")
	}
	oc.noteUserActivity(portal.MXID)

	logCtx := oc.loggerForContext(ctx).With().
		Stringer("event_id", msg.Event.ID).
		Stringer("sender", msg.Event.Sender).
		Stringer("portal", portal.PortalKey).
		Logger()
	ctx = logCtx.WithContext(ctx)

	// Track last active room per agent for heartbeat routing
	oc.recordAgentActivity(ctx, portal, meta)

	// Check deduplication - skip if we've already processed this event
	if oc.inboundDedupeCache != nil {
		dedupeKey := oc.buildDedupeKey(portal.MXID, msg.Event.ID)
		if oc.inboundDedupeCache.Check(dedupeKey) {
			logCtx.Debug().Msg("Skipping duplicate message")
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
	}

	if agentremote.IsMatrixBotUser(ctx, oc.UserLogin.Bridge, msg.Event.Sender) {
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
		oc.sendPendingStatus(ctx, portal, msg.Event, "Processing...")
		pendingSent := true
		return oc.handleMediaMessage(ctx, msg, portal, meta, msgType, pendingSent)
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		// Continue to text handling below
	default:
		logCtx.Debug().Str("msg_type", string(msgType)).Msg("Unsupported message type")
		return nil, agentremote.UnsupportedMessageStatus(fmt.Errorf("%s messages are not supported", msgType))
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

	queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(ctx, portal, meta, "", airuntime.QueueInlineOptions{})

	commandBody := rawBody
	if isGroup {
		commandBody = stripMentionPatterns(commandBody, mc.MentionRegexes)
	}
	if !commandAuthorized && airuntime.IsAbortTriggerText(commandBody) {
		logCtx.Debug().Msg("Ignoring abort trigger from unauthorized sender")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	if commandAuthorized && airuntime.IsAbortTriggerText(commandBody) {
		stopped := oc.abortRoom(ctx, portal, meta)
		oc.sendSystemNotice(ctx, portal, formatAbortNotice(stopped))
		logCtx.Debug().Msg("Abort trigger handled")
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

	runMeta := meta
	runCtx := ctx

	if rawBody == "" {
		return nil, agentremote.UnsupportedMessageStatus(errors.New("empty messages are not supported"))
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

	// Ack reaction (OpenClaw-style scope gating)
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
		oc.storeAckReaction(ctx, portal.MXID, msg.Event.ID, ackReaction)
	}
	body := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, rawBody, senderName, roomName, isGroup)
	inboundCtx := oc.buildMatrixInboundContext(portal, msg.Event, rawBody, senderName, roomName, isGroup)
	runCtx = withInboundContext(runCtx, inboundCtx)
	if isGroup && requireMention {
		body = oc.buildGroupHistoryContext(portal.MXID, body, oc.resolveGroupHistoryLimit())
	}

	// Check if this message should be debounced
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
			PendingSent:  pendingSent,
		}
		// Let the client know the message is pending due to debounce.
		if debounceDelay >= 0 && !pendingSent {
			oc.sendPendingStatus(ctx, portal, msg.Event, "Combining messages...")
			entry.PendingSent = true
		}
		oc.inboundDebouncer.EnqueueWithDelay(debounceKey, entry, true, debounceDelay)
		return &bridgev2.MatrixMessageResponse{Pending: true}, nil
	}
	if debounceKey != "" {
		// Flush any pending debounced messages for this room+sender before immediate processing
		oc.inboundDebouncer.flush(debounceKey)
	}

	// Not debouncing - process immediately
	// Get raw event content for link previews
	var rawEventContent map[string]any
	if msg.Event != nil && msg.Event.Content.Raw != nil {
		rawEventContent = msg.Event.Content.Raw
	}

	eventID := id.EventID("")
	if msg.Event != nil {
		eventID = msg.Event.ID
	}

	promptContext, err := oc.buildContextWithLinkContext(runCtx, portal, runMeta, body, rawEventContent, eventID)
	if err != nil {
		return nil, messageSendStatusError(err, "Couldn't prepare the message. Try again.", "")
	}
	logCtx.Debug().Int("prompt_messages", len(promptContext.Messages)).Msg("Built prompt for inbound message")
	userMessage := &database.Message{
		ID:       agentremote.MatrixMessageID(eventID),
		MXID:     eventID,
		Room:     portal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: agentremote.BaseMessageMetadata{Role: "user", Body: body},
		},
		Timestamp: agentremote.MatrixEventTimestamp(msg.Event),
	}
	setCanonicalTurnDataFromPromptMessages(userMessage.Metadata.(*MessageMetadata), promptTail(promptContext, 1))
	if msg.InputTransactionID != "" {
		userMessage.SendTxnID = networkid.RawTransactionID(msg.InputTransactionID)
	}

	pending := pendingMessage{
		Event:           msg.Event,
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
	queueItem := pendingQueueItem{
		pending:         pending,
		messageID:       string(eventID),
		summaryLine:     rawBodyOriginal,
		enqueuedAt:      time.Now().UnixMilli(),
		rawEventContent: rawEventContent,
	}
	dbMsg, isPending := oc.dispatchOrQueue(runCtx, msg.Event, portal, runMeta, userMessage, queueItem, queueSettings, promptContext)

	return &bridgev2.MatrixMessageResponse{
		DB:      dbMsg,
		Pending: isPending,
	}, nil
}

// HandleMatrixTyping tracks local user typing state for auto-greeting delays.
func (oc *AIClient) HandleMatrixTyping(ctx context.Context, typing *bridgev2.MatrixTyping) error {
	if typing == nil || typing.Portal == nil {
		return nil
	}
	if typing.Portal.MXID == "" {
		return nil
	}
	oc.setUserTyping(typing.Portal.MXID, typing.IsTyping)
	return nil
}

// HandleMatrixEdit handles edits to previously sent messages
func (oc *AIClient) HandleMatrixEdit(ctx context.Context, edit *bridgev2.MatrixEdit) error {
	if edit.Content == nil || edit.EditTarget == nil {
		return errors.New("invalid edit: missing content or target")
	}

	portal := edit.Portal
	if portal == nil {
		return errors.New("portal is nil")
	}
	meta := portalMeta(portal)

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
	msgMeta.Body = newBody

	// Persist the updated metadata
	if err := oc.UserLogin.Bridge.DB.Message.Update(ctx, edit.EditTarget); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist edited message metadata")
	}
	oc.notifySessionMutation(ctx, portal, meta, true)

	// Only regenerate if this was a user message
	if msgMeta.Role != "user" {
		// Just update the content, don't regenerate
		return nil
	}

	oc.loggerForContext(ctx).Info().
		Str("message_id", string(edit.EditTarget.ID)).
		Int("new_body_len", len(newBody)).
		Msg("User edited message, regenerating response")

	// Find the assistant response that came after this message
	// We'll delete it and regenerate
	err := oc.regenerateFromEdit(ctx, edit.Event, portal, meta, edit.EditTarget, newBody)
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
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 50)
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

	// Build the prompt with the edited message included
	// We need to rebuild from scratch up to the edited message
	promptContext, err := oc.buildContextUpToMessage(ctx, portal, meta, editedMessage.ID, newBody)
	if err != nil {
		return fmt.Errorf("failed to build prompt: %w", err)
	}

	// If we found an assistant response, we'll redact/edit it
	if assistantResponse != nil {
		// Try to redact the old response
		if assistantResponse.MXID != "" {
			_ = oc.redactEventViaPortal(ctx, portal, assistantResponse.MXID)
		}
		// Clean up database record to prevent orphaned messages
		if err := oc.UserLogin.Bridge.DB.Message.Delete(ctx, assistantResponse.RowID); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Str("msg_id", string(assistantResponse.ID)).Msg("Failed to delete redacted message from database")
		}
		oc.notifySessionMutation(ctx, portal, meta, true)
	}

	queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(ctx, portal, meta, "", airuntime.QueueInlineOptions{})
	isGroup := oc.isGroupChat(ctx, portal)
	pending := pendingMessage{
		Event:       evt,
		Portal:      portal,
		Meta:        meta,
		Type:        pendingTypeEditRegenerate,
		MessageBody: newBody,
		TargetMsgID: editedMessage.ID,
		Typing: &TypingContext{
			IsGroup:      isGroup,
			WasMentioned: true,
		},
	}
	queueItem := pendingQueueItem{
		pending:     pending,
		messageID:   string(evt.ID),
		summaryLine: newBody,
		enqueuedAt:  time.Now().UnixMilli(),
	}
	oc.dispatchOrQueueCore(ctx, evt, portal, meta, nil, queueItem, queueSettings, promptContext)

	return nil
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
		return nil, agentremote.UnsupportedMessageStatus(fmt.Errorf("%s message has no URL", msgType))
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
		case isTextFileMime(mimeType):
			if !oc.canUseMediaUnderstanding(meta) {
				return nil, agentremote.UnsupportedMessageStatus(errors.New("text file understanding is only available when an agent is assigned"))
			}
			return oc.handleTextFileMessage(ctx, msg, portal, meta, string(mediaURL), mimeType, pendingSent)
		case mimeType == "" || mimeType == "application/octet-stream":
			if !oc.canUseMediaUnderstanding(meta) {
				return nil, agentremote.UnsupportedMessageStatus(errors.New("text file understanding is only available when an agent is assigned"))
			}
			return oc.handleTextFileMessage(ctx, msg, portal, meta, string(mediaURL), mimeType, pendingSent)
		}
	}

	if !ok {
		return nil, agentremote.UnsupportedMessageStatus(fmt.Errorf("unsupported media type: %s", msgType))
	}

	if mimeType == "" {
		mimeType = config.defaultMimeType
	}

	eventID := id.EventID("")
	if msg.Event != nil {
		eventID = msg.Event.ID
	}

	// Check capability (PDF has special OpenRouter handling via file-parser plugin)
	modelCaps := oc.getModelCapabilitiesForMeta(meta)
	supportsMedia := config.capabilityCheck(&modelCaps)
	if isPDF && !supportsMedia && oc.isOpenRouterProvider() {
		supportsMedia = true // OpenRouter supports PDF via file-parser plugin
	}
	queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(ctx, portal, meta, "", airuntime.QueueInlineOptions{})

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

	dispatchTextOnly := func(rawBody string) (*bridgev2.MatrixMessageResponse, error) {
		inboundCtx := oc.buildMatrixInboundContext(portal, msg.Event, rawBody, senderName, roomName, isGroup)
		promptCtx := withInboundContext(ctx, inboundCtx)
		body := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, rawBody, senderName, roomName, isGroup)
		promptContext, err := oc.buildContextWithLinkContext(promptCtx, portal, meta, body, nil, eventID)
		if err != nil {
			return nil, messageSendStatusError(err, "Couldn't prepare the message. Try again.", "")
		}
		userMessage := &database.Message{
			ID:       agentremote.MatrixMessageID(eventID),
			MXID:     eventID,
			Room:     portal.PortalKey,
			SenderID: humanUserID(oc.UserLogin.ID),
			Metadata: &MessageMetadata{
				BaseMessageMetadata: agentremote.BaseMessageMetadata{Role: "user", Body: body},
			},
			Timestamp: agentremote.MatrixEventTimestamp(msg.Event),
		}
		setCanonicalTurnDataFromPromptMessages(userMessage.Metadata.(*MessageMetadata), promptTail(promptContext, 1))
		if msg.InputTransactionID != "" {
			userMessage.SendTxnID = networkid.RawTransactionID(msg.InputTransactionID)
		}
		pending := pendingMessage{
			Event:          msg.Event,
			Portal:         portal,
			Meta:           meta,
			InboundContext: &inboundCtx,
			Type:           pendingTypeText,
			MessageBody:    body,
			PendingSent:    pendingSent,
			Typing:         typingCtx,
		}
		queueItem := pendingQueueItem{
			pending:     pending,
			messageID:   string(eventID),
			summaryLine: rawBody,
			enqueuedAt:  time.Now().UnixMilli(),
		}
		dbMsg, isPending := oc.dispatchOrQueue(promptCtx, msg.Event, portal, meta, userMessage, queueItem, queueSettings, promptContext)
		return &bridgev2.MatrixMessageResponse{
			DB:      dbMsg,
			Pending: isPending,
		}, nil
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
		return nil, agentremote.UnsupportedMessageStatus(fmt.Errorf(
			"%s messages must be preprocessed into text before generation; configure media understanding or upload a transcript",
			msgType,
		))
	}

	if !supportsMedia {
		if understanding != nil && strings.TrimSpace(understanding.Body) != "" {
			return dispatchTextOnly(understanding.Body)
		}

		// If model lacks vision but agent supports image understanding, analyze image first.
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

		// If model lacks audio but agent supports audio understanding, analyze audio first.
		if msgType == event.MsgAudio {
			audioModel, audioFallback := oc.resolveModelForCapability(ctx, meta, func(caps ModelCapabilities) bool { return caps.SupportsAudio }, oc.resolveAudioUnderstandingModel)
			if resp, err := oc.dispatchMediaUnderstandingFallback(
				ctx,
				audioModel,
				audioFallback,
				string(mediaURL),
				mimeType,
				encryptedFile,
				caption,
				hasUserCaption,
				buildMediaUnderstandingPrompt(MediaCapabilityAudio),
				oc.analyzeAudioWithModel,
				buildMediaUnderstandingMessage("Audio", "Transcript"),
				"Audio understanding failed",
				"audio understanding produced empty result",
				"Couldn't analyze the audio. Try again, or switch to an audio-capable model with !ai model.",
				dispatchTextOnly,
			); resp != nil || err != nil {
				return resp, err
			}
		}

		return nil, agentremote.UnsupportedMessageStatus(fmt.Errorf(
			"current model (%s) does not support %s; switch to a capable model using !ai model",
			oc.effectiveModel(meta), config.capabilityName,
		))
	}

	// Build prompt with media
	captionForPrompt := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, caption, senderName, roomName, isGroup)
	captionInboundCtx := oc.buildMatrixInboundContext(portal, msg.Event, caption, senderName, roomName, isGroup)
	promptCtx := withInboundContext(ctx, captionInboundCtx)
	promptContext, err := oc.buildContextWithMedia(promptCtx, portal, meta, captionForPrompt, string(mediaURL), mimeType, encryptedFile, config.msgType, eventID)
	if err != nil {
		return nil, messageSendStatusError(err, "Couldn't prepare the media message. Try again.", "")
	}

	userMeta := &MessageMetadata{
		BaseMessageMetadata: agentremote.BaseMessageMetadata{
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
	setCanonicalTurnDataFromPromptMessages(userMeta, promptTail(promptContext, 1))

	userMessage := &database.Message{
		ID:        agentremote.MatrixMessageID(eventID),
		MXID:      eventID,
		Room:      portal.PortalKey,
		SenderID:  humanUserID(oc.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: agentremote.MatrixEventTimestamp(msg.Event),
	}
	if msg.InputTransactionID != "" {
		userMessage.SendTxnID = networkid.RawTransactionID(msg.InputTransactionID)
	}

	pending := pendingMessage{
		Event:          msg.Event,
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
	queueItem := pendingQueueItem{
		pending:     pending,
		messageID:   string(eventID),
		summaryLine: rawCaption,
		enqueuedAt:  time.Now().UnixMilli(),
	}
	dbMsg, isPending := oc.dispatchOrQueue(promptCtx, msg.Event, portal, meta, userMessage, queueItem, queueSettings, promptContext)

	return &bridgev2.MatrixMessageResponse{
		DB:      dbMsg,
		Pending: isPending,
	}, nil
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
		return nil, messageSendStatusError(err, userError, "")
	}
	if description == "" {
		return nil, messageSendStatusError(errors.New(emptyResult), userError, "")
	}

	combined := buildMessage(caption, hasUserCaption, description)
	if combined == "" {
		return nil, messageSendStatusError(errors.New(emptyResult), userError, "")
	}
	return dispatchTextOnly(combined)
}

func (oc *AIClient) handleTextFileMessage(
	ctx context.Context,
	msg *bridgev2.MatrixMessage,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	mediaURL string,
	mimeType string,
	pendingSent bool,
) (*bridgev2.MatrixMessageResponse, error) {
	if msg == nil {
		return nil, errors.New("missing matrix event for text file message")
	}
	queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(ctx, portal, meta, "", airuntime.QueueInlineOptions{})

	rawCaption := strings.TrimSpace(msg.Content.Body)
	fileName := strings.TrimSpace(msg.Content.FileName)
	hasUserCaption := rawCaption != ""
	if fileName == "" {
		fileName = rawCaption
		hasUserCaption = false
	}
	if rawCaption == fileName {
		hasUserCaption = false
	}
	caption := rawCaption
	if !hasUserCaption {
		caption = "Please analyze this text file."
	}

	isGroup := oc.isGroupChat(ctx, portal)
	roomName := ""
	if isGroup {
		roomName = oc.matrixRoomDisplayName(ctx, portal)
	}
	senderName := oc.matrixDisplayName(ctx, portal.MXID, msg.Event.Sender)
	mc := oc.resolveMentionContext(ctx, portal, meta, msg.Event, msg.Content.Mentions, rawCaption)
	typingCtx := &TypingContext{IsGroup: isGroup, WasMentioned: mc.WasMentioned}

	var encryptedFile *event.EncryptedFileInfo
	if msg.Content.File != nil {
		encryptedFile = msg.Content.File
	}

	content, truncated, err := oc.downloadTextFile(ctx, mediaURL, encryptedFile, mimeType)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Text file understanding failed")
		return nil, messageSendStatusError(err, "Couldn't read the text file. Upload a UTF-8 text file under 5 MB.", "")
	}

	combined := buildTextFileMessage(caption, hasUserCaption, fileName, mimeType, content, truncated)
	if combined == "" {
		return nil, messageSendStatusError(errors.New("text file understanding produced empty result"), "Couldn't read the text file. Upload a UTF-8 text file under 5 MB.", "")
	}

	eventID := id.EventID("")
	if msg.Event != nil {
		eventID = msg.Event.ID
	}

	inboundCtx := oc.buildMatrixInboundContext(portal, msg.Event, combined, senderName, roomName, isGroup)
	promptCtx := withInboundContext(ctx, inboundCtx)
	promptContext, err := oc.buildContextWithLinkContext(promptCtx, portal, meta, combined, nil, eventID)
	if err != nil {
		return nil, messageSendStatusError(err, "Couldn't prepare the message. Try again.", "")
	}

	userMessage := &database.Message{
		ID:       agentremote.MatrixMessageID(eventID),
		MXID:     eventID,
		Room:     portal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: agentremote.BaseMessageMetadata{Role: "user", Body: combined},
		},
		Timestamp: agentremote.MatrixEventTimestamp(msg.Event),
	}
	setCanonicalTurnDataFromPromptMessages(userMessage.Metadata.(*MessageMetadata), promptTail(promptContext, 1))
	if msg.InputTransactionID != "" {
		userMessage.SendTxnID = networkid.RawTransactionID(msg.InputTransactionID)
	}

	pending := pendingMessage{
		Event:          msg.Event,
		Portal:         portal,
		Meta:           meta,
		InboundContext: &inboundCtx,
		Type:           pendingTypeText,
		MessageBody:    combined,
		PendingSent:    pendingSent,
		Typing:         typingCtx,
	}
	queueItem := pendingQueueItem{
		pending:     pending,
		messageID:   string(eventID),
		summaryLine: strings.TrimSpace(rawCaption),
		enqueuedAt:  time.Now().UnixMilli(),
	}
	dbMsg, isPending := oc.dispatchOrQueue(promptCtx, msg.Event, portal, meta, userMessage, queueItem, queueSettings, promptContext)

	return &bridgev2.MatrixMessageResponse{
		DB:      dbMsg,
		Pending: isPending,
	}, nil
}

// savePortalQuiet saves portal and logs errors without failing
func (oc *AIClient) savePortalQuiet(ctx context.Context, portal *bridgev2.Portal, action string) {
	if err := portal.Save(ctx); err != nil {
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

	targetPart, err := oc.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, targetEventID)
	if err != nil || targetPart == nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("target_event", targetEventID).Msg("Target message not found for ack reaction")
		return ""
	}

	sender := oc.senderForPortal(ctx, portal)
	emojiID := networkid.EmojiID(emoji)
	result := oc.UserLogin.QueueRemoteEvent(agentremote.BuildReactionEvent(
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
func (oc *AIClient) storeAckReaction(ctx context.Context, roomID id.RoomID, sourceEventID id.EventID, emoji string) {
	// Look up the network message ID for the source event
	var targetNetworkID networkid.MessageID
	if part, err := oc.UserLogin.Bridge.DB.Message.GetPartByMXID(ctx, sourceEventID); err == nil && part != nil {
		targetNetworkID = part.ID
	}

	ackReactionStoreMu.Lock()
	defer ackReactionStoreMu.Unlock()

	if ackReactionStore[roomID] == nil {
		ackReactionStore[roomID] = make(map[id.EventID]ackReactionEntry)
	}
	ackReactionStore[roomID][sourceEventID] = ackReactionEntry{
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
	oc.UserLogin.QueueRemoteEvent(agentremote.BuildReactionRemoveEvent(
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
	latestUserID id.EventID,
) (PromptContext, error) {
	var promptContext PromptContext
	bridgesdk.AppendChatMessagesToPromptContext(&promptContext.PromptContext, oc.buildSystemMessages(ctx, portal, meta))

	historyLimit := oc.historyLimit(ctx, portal, meta)
	resetAt := int64(0)
	if meta != nil {
		resetAt = meta.SessionResetAt
	}
	if historyLimit > 0 {
		history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, historyLimit+2)
		if err != nil {
			return PromptContext{}, fmt.Errorf("failed to load prompt history: %w", err)
		}

		// Determine whether to inject images into history (requires vision-capable model).
		hasVision := oc.getModelCapabilitiesForMeta(meta).SupportsVision
		historyBundles := make([][]PromptMessage, 0, len(history))

		// Skip the most recent messages (last user and assistant) and build from older history
		skippedUser := false
		skippedAssistant := false
		includedCount := 0
		for _, msg := range history {
			msgMeta := messageMeta(msg)
			// Skip commands and non-conversation messages
			if !shouldIncludeInHistory(msgMeta) {
				continue
			}
			if resetAt > 0 && msg.Timestamp.UnixMilli() < resetAt {
				continue
			}

			// Skip the last user message and last assistant message
			if !skippedUser && msgMeta.Role == "user" {
				skippedUser = true
				continue
			}
			if !skippedAssistant && msgMeta.Role == "assistant" {
				skippedAssistant = true
				continue
			}

			// Only inject images for recent messages and vision-capable models.
			// This loop builds newest-to-oldest, so early entries are the most recent.
			injectImages := hasVision && includedCount < maxHistoryImageMessages
			includedCount++
			bundle := oc.historyMessageBundle(ctx, msgMeta, injectImages)
			if len(bundle) > 0 {
				historyBundles = append(historyBundles, bundle)
			}
		}

		for i := len(historyBundles) - 1; i >= 0; i-- {
			promptContext.Messages = append(promptContext.Messages, historyBundles[i]...)
		}
	}

	latest := latestUserBody
	promptContext.Messages = append(promptContext.Messages, PromptMessage{
		Role: PromptRoleUser,
		Blocks: []PromptBlock{{
			Type: PromptBlockText,
			Text: latest,
		}},
	})
	return promptContext, nil
}
