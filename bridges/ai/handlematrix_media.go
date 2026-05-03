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
	queueSettings := resolveQueueSettings(queueResolveParams{cfg: cfg, inlineOpts: airuntime.QueueInlineOptions{}})

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
