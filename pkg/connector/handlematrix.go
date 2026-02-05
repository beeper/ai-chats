package connector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func unsupportedMessageStatus(err error) error {
	return bridgev2.WrapErrorInStatus(err).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusUnsupported).
		WithIsCertain(true).
		WithSendNotice(true).
		WithErrorAsMessage()
}

// HandleMatrixMessage processes incoming Matrix messages and dispatches them to the AI
func (oc *AIClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if msg.Content == nil {
		return nil, fmt.Errorf("missing message content")
	}

	portal := msg.Portal
	if portal == nil {
		return nil, fmt.Errorf("portal is nil")
	}
	meta := portalMeta(portal)
	if msg.Event == nil {
		return nil, fmt.Errorf("missing message event")
	}

	// Track last active room per agent for heartbeat routing
	oc.recordAgentActivity(ctx, portal, meta)

	// Check deduplication - skip if we've already processed this event
	if msg.Event != nil && oc.inboundDedupeCache != nil {
		dedupeKey := oc.buildDedupeKey(portal.MXID, msg.Event.ID)
		if oc.inboundDedupeCache.Check(dedupeKey) {
			oc.log.Debug().Stringer("event_id", msg.Event.ID).Msg("Skipping duplicate message")
			return &bridgev2.MatrixMessageResponse{Pending: false}, nil
		}
	}

	if oc.isMatrixBotUser(ctx, msg.Event.Sender) {
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
		if mimeType := normalizeMimeType(msg.Content.Info.MimeType); strings.HasPrefix(mimeType, "image/") {
			msgType = event.MsgImage
		}
	}

	// Handle media messages based on type (media is never debounced)
	switch msgType {
	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		// Flush any pending debounced messages for this room+sender before processing media
		if oc.inboundDebouncer != nil {
			debounceKey := BuildDebounceKey(portal.MXID, msg.Event.Sender)
			oc.inboundDebouncer.FlushKey(debounceKey)
		}
		return oc.handleMediaMessage(ctx, msg, portal, meta, msgType)
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		// Continue to text handling below
	default:
		return nil, unsupportedMessageStatus(fmt.Errorf("%s messages are not supported", msgType))
	}
<<<<<<< ours
	body := strings.TrimSpace(msg.Content.Body)
	if body == "" {
		return nil, unsupportedMessageStatus(fmt.Errorf("empty messages are not supported"))
=======
	if msg.Content.RelatesTo != nil && msg.Content.RelatesTo.GetReplaceID() != "" {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	rawBody := strings.TrimSpace(msg.Content.Body)
	if msg.Content.MsgType == event.MsgLocation && strings.TrimSpace(msg.Content.GeoURI) != "" {
		rawMap := msg.Event.Content.Raw
		if loc := resolveMatrixLocation(rawMap); loc != nil && strings.TrimSpace(loc.Text) != "" {
			rawBody = loc.Text
		}
	}
	if rawBody == "" {
		return nil, fmt.Errorf("empty messages are not supported")
>>>>>>> theirs
	}

	isGroup := oc.isGroupChat(ctx, portal)
	roomName := ""
	if isGroup {
		roomName = oc.matrixRoomDisplayName(ctx, portal)
	}
	senderName := oc.matrixDisplayName(ctx, portal.MXID, msg.Event.Sender)

	// Mention detection (OpenClaw-style)
	botMXID := oc.resolveBotMXID(ctx, portal, meta)
	explicitMention := false
	hasExplicit := false
	if msg.Content.Mentions != nil {
		hasExplicit = true
		if msg.Content.Mentions.Room || (botMXID != "" && msg.Content.Mentions.Has(botMXID)) {
			explicitMention = true
		}
	}
	var agentDef *agents.AgentDefinition
	if agentID := resolveAgentID(meta); agentID != "" {
		store := NewAgentStoreAdapter(oc)
		if agent, err := store.GetAgentByID(ctx, agentID); err == nil {
			agentDef = agent
		}
	}
	mentionRegexes := buildMentionRegexes(&oc.connector.Config, agentDef)
	wasMentioned := explicitMention || matchesMentionPatterns(rawBody, mentionRegexes)
	requireMention := isGroup
	canDetectMention := len(mentionRegexes) > 0 || hasExplicit
	shouldBypassMention := false
	if isGroup && requireMention && !wasMentioned && !shouldBypassMention {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}

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
		oc.storeAckReaction(portal.MXID, msg.Event.ID, ackReactionEventID)
	}

	body := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, rawBody, senderName, roomName, isGroup)

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
		entry := DebounceEntry{
			Event:      msg.Event,
			Portal:     portal,
			Meta:       meta,
			RawBody:    rawBody,
			SenderName: senderName,
			RoomName:   roomName,
			IsGroup:    isGroup,
			AckEventID: ackReactionEventID,
		}
		oc.inboundDebouncer.EnqueueWithDelay(debounceKey, entry, true, debounceDelay)

<<<<<<< ours
		// Enqueue to debouncer - processing happens after delay
		// Use per-room debounce delay if configured (0 = default, -1 = disabled)
		debounceKey := BuildDebounceKey(portal.MXID, msg.Event.Sender)
		oc.inboundDebouncer.EnqueueWithDelay(debounceKey, entry, true, meta.DebounceMs)

		// Let the client know the message is pending due to debounce.
		if meta.DebounceMs >= 0 {
			oc.sendPendingStatus(ctx, portal, msg.Event, "Combining messages...")
			entry.PendingSent = true
		}

		// Return Pending=true since we're handling this asynchronously
		return &bridgev2.MatrixMessageResponse{
			Pending: true,
		}, nil
=======
		return &bridgev2.MatrixMessageResponse{Pending: true}, nil
	}
	if debounceKey != "" {
		// Flush any pending debounced messages for this room+sender before immediate processing
		oc.inboundDebouncer.FlushKey(debounceKey)
>>>>>>> theirs
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

	promptMessages, err := oc.buildPromptWithLinkContext(ctx, portal, meta, body, rawEventContent, eventID)
	if err != nil {
		return nil, err
	}
	userMessage := &database.Message{
		ID:       networkid.MessageID(fmt.Sprintf("mx:%s", string(eventID))),
		MXID:     eventID,
		Room:     portal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: &MessageMetadata{
			Role: "user",
			Body: body,
		},
		Timestamp: time.Now(),
	}

	dbMsg, isPending := oc.dispatchOrQueue(ctx, msg.Event, portal, meta, userMessage, pendingMessage{
		Event:       msg.Event,
		Portal:      portal,
		Meta:        meta,
		Type:        pendingTypeText,
		MessageBody: body,
	}, promptMessages)

	return &bridgev2.MatrixMessageResponse{
		DB:      dbMsg,
		Pending: isPending,
	}, nil
}

// HandleMatrixEdit handles edits to previously sent messages
func (oc *AIClient) HandleMatrixEdit(ctx context.Context, edit *bridgev2.MatrixEdit) error {
	if edit.Content == nil || edit.EditTarget == nil {
		return fmt.Errorf("invalid edit: missing content or target")
	}

	portal := edit.Portal
	if portal == nil {
		return fmt.Errorf("portal is nil")
	}
	meta := portalMeta(portal)

	// Get the new message body
	newBody := strings.TrimSpace(edit.Content.Body)
	if newBody == "" {
		return fmt.Errorf("empty edit body")
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
		oc.log.Warn().Err(err).Msg("Failed to persist edited message metadata")
	}
	oc.notifySessionMemoryChange(ctx, portal, meta, true)

	// Only regenerate if this was a user message
	if msgMeta.Role != "user" {
		// Just update the content, don't regenerate
		return nil
	}

	oc.log.Info().
		Str("message_id", string(edit.EditTarget.ID)).
		Str("new_body", newBody).
		Msg("User edited message, regenerating response")

	// Find the assistant response that came after this message
	// We'll delete it and regenerate
	err := oc.regenerateFromEdit(ctx, edit.Event, portal, meta, edit.EditTarget, newBody)
	if err != nil {
		oc.log.Err(err).Msg("Failed to regenerate response after edit")
		oc.sendSystemNotice(ctx, portal, fmt.Sprintf("Failed to regenerate response: %v", err))
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
	promptMessages, err := oc.buildPromptUpToMessage(ctx, portal, meta, editedMessage.ID, newBody)
	if err != nil {
		return fmt.Errorf("failed to build prompt: %w", err)
	}

	// If we found an assistant response, we'll redact/edit it
	if assistantResponse != nil {
		// Try to redact the old response
		if assistantResponse.MXID != "" {
			intent, _ := portal.GetIntentFor(ctx, bridgev2.EventSender{IsFromMe: true}, oc.UserLogin, bridgev2.RemoteEventMessageRemove)
			if intent != nil {
				_, _ = intent.SendMessage(ctx, portal.MXID, event.EventRedaction, &event.Content{
					Parsed: &event.RedactionEventContent{
						Redacts: assistantResponse.MXID,
					},
				}, nil)
			}
		}
		// Clean up database record to prevent orphaned messages
		if err := oc.UserLogin.Bridge.DB.Message.Delete(ctx, assistantResponse.RowID); err != nil {
			oc.log.Warn().Err(err).Str("msg_id", string(assistantResponse.ID)).Msg("Failed to delete redacted message from database")
		}
		oc.notifySessionMemoryChange(ctx, portal, meta, true)
	}

	oc.dispatchOrQueueWithStatus(ctx, evt, portal, meta, pendingMessage{
		Event:       evt,
		Portal:      portal,
		Meta:        meta,
		Type:        pendingTypeEditRegenerate,
		MessageBody: newBody,
		TargetMsgID: editedMessage.ID,
	}, promptMessages)

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
) (*bridgev2.MatrixMessageResponse, error) {
	if msg.Event == nil {
		return nil, fmt.Errorf("missing message event")
	}
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
		return nil, unsupportedMessageStatus(fmt.Errorf("%s message has no URL", msgType))
	}

	// Get MIME type
	mimeType := ""
	if msg.Content.Info != nil && msg.Content.Info.MimeType != "" {
		mimeType = normalizeMimeType(msg.Content.Info.MimeType)
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
				oc.sendSystemNotice(ctx, portal, "Text file understanding is only available when an agent is assigned and raw mode is off.")
				return &bridgev2.MatrixMessageResponse{}, nil
			}
			return oc.handleTextFileMessage(ctx, msg, portal, meta, string(mediaURL), mimeType)
		case mimeType == "" || mimeType == "application/octet-stream":
			if !oc.canUseMediaUnderstanding(meta) {
				oc.sendSystemNotice(ctx, portal, "Text file understanding is only available when an agent is assigned and raw mode is off.")
				return &bridgev2.MatrixMessageResponse{}, nil
			}
			return oc.handleTextFileMessage(ctx, msg, portal, meta, string(mediaURL), mimeType)
		}
	}

	if !ok {
		return nil, unsupportedMessageStatus(fmt.Errorf("unsupported media type: %s", msgType))
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

	// Get encrypted file info if present (for E2EE rooms)
	var encryptedFile *event.EncryptedFileInfo
	if msg.Content.File != nil {
		encryptedFile = msg.Content.File
	}

	dispatchTextOnly := func(rawBody string) (*bridgev2.MatrixMessageResponse, error) {
		body := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, rawBody, senderName, roomName, isGroup)
		promptMessages, err := oc.buildPrompt(ctx, portal, meta, body, eventID)
		if err != nil {
			return nil, err
		}
		userMessage := &database.Message{
			ID:       networkid.MessageID(fmt.Sprintf("mx:%s", string(eventID))),
			MXID:     eventID,
			Room:     portal.PortalKey,
			SenderID: humanUserID(oc.UserLogin.ID),
			Metadata: &MessageMetadata{
				Role: "user",
				Body: body,
			},
			Timestamp: time.Now(),
		}
		dbMsg, isPending := oc.dispatchOrQueue(ctx, msg.Event, portal, meta, userMessage, pendingMessage{
			Event:       msg.Event,
			Portal:      portal,
			Meta:        meta,
			Type:        pendingTypeText,
			MessageBody: body,
		}, promptMessages)
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
			oc.log.Warn().Err(err).Msg("Media understanding failed")
		} else if result != nil {
			understanding = result
			if strings.TrimSpace(result.Body) != "" {
				caption = result.Body
			}
		}
	}

	if !supportsMedia {
		if understanding != nil && strings.TrimSpace(understanding.Body) != "" {
			return dispatchTextOnly(understanding.Body)
		}

		// If model lacks vision but agent supports image understanding, analyze image first.
		if msgType == event.MsgImage {
			visionModel, visionFallback := oc.resolveVisionModelForImage(ctx, meta)
			if visionFallback && visionModel != "" {
				analysisPrompt := buildImageUnderstandingPrompt(caption, hasUserCaption)
				description, err := oc.analyzeImageWithModel(ctx, visionModel, string(mediaURL), mimeType, encryptedFile, analysisPrompt)
				if err != nil {
					oc.log.Warn().Err(err).Msg("Image understanding failed")
					oc.sendSystemNotice(ctx, portal, "Image understanding failed. Please try again or switch to a vision-capable model using /model.")
					return &bridgev2.MatrixMessageResponse{}, nil
				}

				combined := buildImageUnderstandingMessage(caption, hasUserCaption, description)
				if combined == "" {
					oc.sendSystemNotice(ctx, portal, "Image understanding failed. Please try again or switch to a vision-capable model using /model.")
					return &bridgev2.MatrixMessageResponse{}, nil
				}
				return dispatchTextOnly(combined)
			}
		}

		// If model lacks audio but agent supports audio understanding, analyze audio first.
		if msgType == event.MsgAudio {
			audioModel, audioFallback := oc.resolveAudioModelForInput(ctx, meta)
			if audioFallback && audioModel != "" {
				analysisPrompt := buildAudioUnderstandingPrompt(caption, hasUserCaption)
				transcript, err := oc.analyzeAudioWithModel(ctx, audioModel, string(mediaURL), mimeType, encryptedFile, analysisPrompt)
				if err != nil {
					oc.log.Warn().Err(err).Msg("Audio understanding failed")
					oc.sendSystemNotice(ctx, portal, "Audio understanding failed. Please try again or switch to an audio-capable model using /model.")
					return &bridgev2.MatrixMessageResponse{}, nil
				}

				combined := buildAudioUnderstandingMessage(caption, hasUserCaption, transcript)
				if combined == "" {
					oc.sendSystemNotice(ctx, portal, "Audio understanding failed. Please try again or switch to an audio-capable model using /model.")
					return &bridgev2.MatrixMessageResponse{}, nil
				}
				return dispatchTextOnly(combined)
			}
		}

		oc.sendSystemNotice(ctx, portal, fmt.Sprintf(
			"The current model (%s) does not support %s. Please switch to a capable model using /model.",
			oc.effectiveModel(meta), config.capabilityName,
		))
		return &bridgev2.MatrixMessageResponse{}, nil
	}

	// Build prompt with media
	captionForPrompt := oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, caption, senderName, roomName, isGroup)
	promptMessages, err := oc.buildPromptWithMedia(ctx, portal, meta, captionForPrompt, string(mediaURL), mimeType, encryptedFile, config.msgType, eventID)
	if err != nil {
		return nil, err
	}

	userMeta := &MessageMetadata{
		Role: "user",
		Body: oc.buildMatrixInboundBody(ctx, portal, meta, msg.Event, buildMediaMetadataBody(caption, config.bodySuffix, understanding), senderName, roomName, isGroup),
	}
	if understanding != nil {
		userMeta.MediaUnderstanding = understanding.Outputs
		userMeta.MediaUnderstandingDecisions = understanding.Decisions
		userMeta.Transcript = understanding.Transcript
	}

	userMessage := &database.Message{
		ID:        networkid.MessageID(fmt.Sprintf("mx:%s", string(eventID))),
		MXID:      eventID,
		Room:      portal.PortalKey,
		SenderID:  humanUserID(oc.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: time.Now(),
	}

	dbMsg, isPending := oc.dispatchOrQueue(ctx, msg.Event, portal, meta, userMessage, pendingMessage{
		Event:         msg.Event,
		Portal:        portal,
		Meta:          meta,
		Type:          config.msgType,
		MessageBody:   captionForPrompt,
		MediaURL:      string(mediaURL),
		MimeType:      mimeType,
		EncryptedFile: encryptedFile,
	}, promptMessages)

	return &bridgev2.MatrixMessageResponse{
		DB:      dbMsg,
		Pending: isPending,
	}, nil
}

func (oc *AIClient) handleTextFileMessage(
	ctx context.Context,
	msg *bridgev2.MatrixMessage,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	mediaURL string,
	mimeType string,
) (*bridgev2.MatrixMessageResponse, error) {
	if msg == nil || msg.Event == nil {
		return nil, fmt.Errorf("missing matrix event for text file message")
	}

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

	var encryptedFile *event.EncryptedFileInfo
	if msg.Content.File != nil {
		encryptedFile = msg.Content.File
	}

	content, truncated, err := oc.downloadTextFile(ctx, mediaURL, encryptedFile, mimeType)
	if err != nil {
		oc.log.Warn().Err(err).Msg("Text file understanding failed")
		oc.sendSystemNotice(ctx, portal, "Text file understanding failed. Please upload a UTF-8 text file under 5 MB.")
		return &bridgev2.MatrixMessageResponse{}, nil
	}

	combined := buildTextFileMessage(caption, hasUserCaption, fileName, mimeType, content, truncated)
	if combined == "" {
		oc.sendSystemNotice(ctx, portal, "Text file understanding failed. Please upload a UTF-8 text file under 5 MB.")
		return &bridgev2.MatrixMessageResponse{}, nil
	}

	eventID := id.EventID("")
	if msg.Event != nil {
		eventID = msg.Event.ID
	}

	promptMessages, err := oc.buildPrompt(ctx, portal, meta, combined, eventID)
	if err != nil {
		return nil, err
	}

	userMessage := &database.Message{
		ID:       networkid.MessageID(fmt.Sprintf("mx:%s", string(eventID))),
		MXID:     eventID,
		Room:     portal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: &MessageMetadata{
			Role: "user",
			Body: combined,
		},
		Timestamp: time.Now(),
	}

	dbMsg, isPending := oc.dispatchOrQueue(ctx, msg.Event, portal, meta, userMessage, pendingMessage{
		Event:       msg.Event,
		Portal:      portal,
		Meta:        meta,
		Type:        pendingTypeText,
		MessageBody: combined,
	}, promptMessages)

	return &bridgev2.MatrixMessageResponse{
		DB:      dbMsg,
		Pending: isPending,
	}, nil
}

// savePortalQuiet saves portal and logs errors without failing
func (oc *AIClient) savePortalQuiet(ctx context.Context, portal *bridgev2.Portal, action string) {
	if err := portal.Save(ctx); err != nil {
		oc.log.Warn().Err(err).Str("action", action).Msg("Failed to save portal")
	}
}

// Ack reaction tracking for removal after reply
// Maps room ID -> source message ID -> ack reaction event ID
const (
	ackReactionTTL             = 5 * time.Minute
	ackReactionCleanupInterval = time.Minute
)

type ackReactionEntry struct {
	reactionEventID id.EventID
	storedAt        time.Time
}

var (
	ackReactionStore   = make(map[id.RoomID]map[id.EventID]ackReactionEntry)
	ackReactionStoreMu sync.Mutex
)

func init() {
	go cleanupAckReactionStore()
}

func cleanupAckReactionStore() {
	ticker := time.NewTicker(ackReactionCleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
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
	}
}

// sendAckReaction sends an acknowledgement reaction to a message.
// Returns the event ID of the reaction for potential removal.
func (oc *AIClient) sendAckReaction(ctx context.Context, portal *bridgev2.Portal, targetEventID id.EventID, emoji string) id.EventID {
	if portal == nil || portal.MXID == "" || targetEventID == "" || emoji == "" {
		return ""
	}
	if err := oc.ensureModelInRoom(ctx, portal); err != nil {
		oc.log.Warn().Err(err).Msg("Failed to ensure ghost is in room for ack reaction")
		return ""
	}
	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return ""
	}

	eventContent := &event.Content{
		Raw: map[string]any{
			"m.relates_to": map[string]any{
				"rel_type": "m.annotation",
				"event_id": targetEventID.String(),
				"key":      emoji,
			},
		},
	}

	resp, err := intent.SendMessage(ctx, portal.MXID, event.EventReaction, eventContent, nil)
	if err != nil {
		oc.log.Warn().Err(err).
			Stringer("target_event", targetEventID).
			Str("emoji", emoji).
			Msg("Failed to send ack reaction")
		return ""
	}

	oc.log.Debug().
		Stringer("target_event", targetEventID).
		Str("emoji", emoji).
		Stringer("reaction_event", resp.EventID).
		Msg("Sent ack reaction")
	return resp.EventID
}

// storeAckReaction stores an ack reaction for later removal.
func (oc *AIClient) storeAckReaction(roomID id.RoomID, sourceEventID, reactionEventID id.EventID) {
	ackReactionStoreMu.Lock()
	defer ackReactionStoreMu.Unlock()

	if ackReactionStore[roomID] == nil {
		ackReactionStore[roomID] = make(map[id.EventID]ackReactionEntry)
	}
	ackReactionStore[roomID][sourceEventID] = ackReactionEntry{
		reactionEventID: reactionEventID,
		storedAt:        time.Now(),
	}
}

// removeAckReaction removes a previously sent ack reaction.
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

	reactionEventID := entry.reactionEventID
	if reactionEventID == "" {
		return
	}

	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return
	}

	// Redact the ack reaction
	_, err := intent.SendMessage(ctx, portal.MXID, event.EventRedaction, &event.Content{
		Parsed: &event.RedactionEventContent{
			Redacts: reactionEventID,
		},
	}, nil)
	if err != nil {
		oc.log.Warn().Err(err).
			Stringer("reaction_event", reactionEventID).
			Msg("Failed to remove ack reaction")
	} else {
		oc.log.Debug().
			Stringer("reaction_event", reactionEventID).
			Msg("Removed ack reaction")
	}
}

// handleToolsCommand handles the /tools command for per-tool management
func (oc *AIClient) handleToolsCommand(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	arg string,
) {
	runCtx := oc.backgroundContext(ctx)

	// No args - show status
	if arg == "" {
		oc.showToolsStatus(runCtx, portal, meta)
		return
	}

	parts := strings.SplitN(arg, " ", 2)
	action := strings.ToLower(parts[0])

	switch action {
	case "list":
		oc.showToolsStatus(runCtx, portal, meta)
	case "on", "enable", "true", "1", "off", "disable", "false", "0":
		oc.sendSystemNotice(runCtx, portal, "Per-tool toggles are no longer supported. Update tool policy in agent settings or the global tool_policy config.")
	default:
		oc.sendSystemNotice(runCtx, portal, "Usage:\n"+
			"• /tools - Show current tool status\n"+
			"• /tools list - List available tools\n"+
			"Tool toggles are managed by tool policy.")
	}
}

// showToolsStatus displays the current status of all tools
func (oc *AIClient) showToolsStatus(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) {
	var sb strings.Builder
	sb.WriteString("Tool Status:\n\n")

	toolsList := oc.buildAvailableTools(meta)
	sort.Slice(toolsList, func(i, j int) bool {
		return toolsList[i].Name < toolsList[j].Name
	})

	sb.WriteString("Tools:\n")
	for _, tool := range toolsList {
		status := "✗"
		if tool.Enabled {
			status = "✓"
		}
		desc := tool.Description
		if desc == "" {
			desc = tool.DisplayName
		}
		reason := ""
		if !tool.Enabled && tool.Reason != "" {
			reason = fmt.Sprintf(" (%s)", tool.Reason)
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s: %s%s\n", status, tool.Name, desc, reason))
	}

	if meta != nil && !meta.Capabilities.SupportsToolCalling {
		sb.WriteString(fmt.Sprintf("\nNote: Current model (%s) may not support tool calling.\n", oc.effectiveModel(meta)))
	}

	oc.sendSystemNotice(ctx, portal, sb.String())
}

// handleRegenerate regenerates the last AI response
func (oc *AIClient) handleRegenerate(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) {
	runCtx := oc.backgroundContext(ctx)
	runCtx = oc.log.WithContext(runCtx)

	// Get message history
	history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(runCtx, portal.PortalKey, 10)
	if err != nil || len(history) == 0 {
		oc.sendSystemNotice(runCtx, portal, "No messages to regenerate from.")
		return
	}

	// Find the last user message
	var lastUserMessage *database.Message
	for _, msg := range history {
		msgMeta := messageMeta(msg)
		if msgMeta != nil && msgMeta.Role == "user" {
			lastUserMessage = msg
			break
		}
	}

	if lastUserMessage == nil {
		oc.sendSystemNotice(runCtx, portal, "No user message found to regenerate from.")
		return
	}

	userMeta := messageMeta(lastUserMessage)
	if userMeta == nil || userMeta.Body == "" {
		oc.sendSystemNotice(runCtx, portal, "Cannot regenerate: message content not available.")
		return
	}

	oc.sendSystemNotice(runCtx, portal, "Regenerating response...")

	// Build prompt excluding the old assistant response
	prompt, err := oc.buildPromptForRegenerate(runCtx, portal, meta, userMeta.Body, lastUserMessage.MXID)
	if err != nil {
		oc.sendSystemNotice(runCtx, portal, "Failed to regenerate: "+err.Error())
		return
	}

	oc.dispatchOrQueueWithStatus(runCtx, evt, portal, meta, pendingMessage{
		Event:         evt,
		Portal:        portal,
		Meta:          meta,
		Type:          pendingTypeRegenerate,
		MessageBody:   userMeta.Body,
		SourceEventID: lastUserMessage.MXID,
	}, prompt)
}

// handleRegenerateTitle regenerates the current room title from recent messages.
func (oc *AIClient) handleRegenerateTitle(
	ctx context.Context,
	portal *bridgev2.Portal,
) {
	runCtx := oc.backgroundContext(ctx)
	runCtx = oc.log.WithContext(runCtx)

	history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(runCtx, portal.PortalKey, 20)
	if err != nil || len(history) == 0 {
		oc.sendSystemNotice(runCtx, portal, "No messages to generate a title from.")
		return
	}

	var lastUserMessage *database.Message
	var lastAssistantMessage *database.Message
	for _, msg := range history {
		msgMeta := messageMeta(msg)
		if !shouldIncludeInHistory(msgMeta) {
			continue
		}
		if lastAssistantMessage == nil && msgMeta.Role == "assistant" {
			lastAssistantMessage = msg
		}
		if lastUserMessage == nil && msgMeta.Role == "user" {
			lastUserMessage = msg
		}
		if lastUserMessage != nil && lastAssistantMessage != nil {
			break
		}
	}

	if lastUserMessage == nil {
		oc.sendSystemNotice(runCtx, portal, "No user message found to generate a title from.")
		return
	}

	userMeta := messageMeta(lastUserMessage)
	if userMeta == nil || userMeta.Body == "" {
		oc.sendSystemNotice(runCtx, portal, "Cannot generate title: message content not available.")
		return
	}

	assistantBody := ""
	if lastAssistantMessage != nil {
		assistantMeta := messageMeta(lastAssistantMessage)
		if assistantMeta != nil {
			assistantBody = assistantMeta.Body
		}
	}

	oc.sendSystemNotice(runCtx, portal, "Regenerating title...")

	title, err := oc.generateRoomTitle(runCtx, userMeta.Body, assistantBody)
	if err != nil {
		oc.sendSystemNotice(runCtx, portal, "Failed to generate title: "+err.Error())
		return
	}

	title = strings.TrimSpace(title)
	if title == "" {
		oc.sendSystemNotice(runCtx, portal, "Failed to generate title: empty response.")
		return
	}

	if err := oc.setRoomName(runCtx, portal, title); err != nil {
		oc.sendSystemNotice(runCtx, portal, "Failed to set room title: "+err.Error())
		return
	}

	oc.sendSystemNotice(runCtx, portal, fmt.Sprintf("Room title updated to: %s", title))
}

// buildPromptForRegenerate builds a prompt for regeneration, excluding the last assistant message
func (oc *AIClient) buildPromptForRegenerate(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	latestUserBody string,
	latestUserID id.EventID,
) ([]openai.ChatCompletionMessageParamUnion, error) {
	var prompt []openai.ChatCompletionMessageParamUnion
	systemPrompt := oc.effectivePrompt(meta)
	if systemPrompt != "" {
		prompt = append(prompt, openai.SystemMessage(systemPrompt))
	}

	historyLimit := oc.historyLimit(meta)
	if historyLimit > 0 {
		history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, historyLimit+2)
		if err != nil {
			return nil, fmt.Errorf("failed to load prompt history: %w", err)
		}

		// Skip the most recent messages (last user and assistant) and build from older history
		skippedUser := false
		skippedAssistant := false
		for _, msg := range history {
			msgMeta := messageMeta(msg)
			// Skip commands and non-conversation messages
			if !shouldIncludeInHistory(msgMeta) {
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

			body := msgMeta.Body
			if msg.MXID != "" {
				body = appendMessageIDHint(msgMeta.Body, msg.MXID)
			}
			switch msgMeta.Role {
			case "assistant":
				prompt = append(prompt, openai.AssistantMessage(body))
			default:
				prompt = append(prompt, openai.UserMessage(body))
			}
		}

		// Reverse to get chronological order (skip system message at index 0 if present)
		startIdx := 0
		if systemPrompt != "" && len(prompt) > 0 {
			startIdx = 1
		}
		for i, j := len(prompt)-1, startIdx; i > j; i, j = i-1, j+1 {
			prompt[i], prompt[j] = prompt[j], prompt[i]
		}
	}

	prompt = append(prompt, openai.UserMessage(appendMessageIDHint(latestUserBody, latestUserID)))
	return prompt, nil
}
