package connector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/connector/msgconv"
	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
	"github.com/beeper/ai-bridge/pkg/shared/citations"
	"github.com/beeper/ai-bridge/pkg/shared/streamtransport"
)

// sendContinuationMessage sends overflow text as a new (non-edit) message from the bot.
func (oc *AIClient) sendContinuationMessage(ctx context.Context, portal *bridgev2.Portal, body string) {
	if portal == nil || portal.MXID == "" {
		return
	}
	meta := portalMeta(portal)
	agentID := resolveAgentID(meta)
	modelID := oc.effectiveModel(meta)
	rendered := msgconv.BuildPlainMessageContent(msgconv.PlainMessageContentParams{
		Text: body,
	})
	// Add continuation flag to the raw content
	rendered.Raw["com.beeper.continuation"] = true
	senderID := modelUserID(modelID)
	if agentID != "" {
		senderID = agentUserID(agentID)
	}
	msg := &AIRemoteMessage{
		portal:    portal.PortalKey,
		id:        newMessageID(),
		sender:    bridgev2.EventSender{Sender: senderID, SenderLogin: oc.UserLogin.ID},
		timestamp: time.Now(),
		variant:   AIMessageText,
		preBuilt: &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{{
				ID:      networkid.PartID("0"),
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgText, Body: body},
				Extra:   rendered.Raw,
			}},
		},
	}
	oc.UserLogin.QueueRemoteEvent(msg)
	oc.loggerForContext(ctx).Debug().Int("body_len", len(body)).Msg("Queued continuation message for oversized response")
}

// sendInitialStreamMessage sends the first message in a streaming session via bridgev2's pipeline.
// Returns the event ID and stores the network message ID in state for later edits.
func (oc *AIClient) sendInitialStreamMessage(ctx context.Context, portal *bridgev2.Portal, state *streamingState, content string, turnID string, replyTarget ReplyTarget) id.EventID {
	var relatesTo map[string]any
	if replyTarget.ThreadRoot != "" {
		replyTo := replyTarget.EffectiveReplyTo()
		relatesTo = map[string]any{
			"rel_type":        RelThread,
			"event_id":        replyTarget.ThreadRoot.String(),
			"is_falling_back": true,
			"m.in_reply_to": map[string]any{
				"event_id": replyTo.String(),
			},
		}
	} else if replyTarget.ReplyTo != "" {
		relatesTo = map[string]any{
			"m.in_reply_to": map[string]any{
				"event_id": replyTarget.ReplyTo.String(),
			},
		}
	}

	uiMessage := map[string]any{
		"id":   turnID,
		"role": "assistant",
		"metadata": map[string]any{
			"turn_id": turnID,
		},
		"parts": []any{},
	}

	eventRaw := map[string]any{
		"msgtype":      event.MsgText,
		"body":         content,
		"m.relates_to": relatesTo,
		BeeperAIKey:    uiMessage,
		"m.mentions":   map[string]any{},
	}

	msgID := newMessageID()
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:         networkid.PartID("0"),
			Type:       event.EventMessage,
			Content:    &event.MessageEventContent{MsgType: event.MsgText, Body: content},
			Extra:      eventRaw,
			DBMetadata: &MessageMetadata{Role: "assistant", TurnID: turnID},
		}},
	}

	eventID, _, err := oc.sendViaPortal(ctx, portal, converted, msgID)
	if err != nil {
		oc.loggerForContext(ctx).Error().Err(err).Msg("Failed to send initial streaming message")
		return ""
	}
	if state != nil {
		state.networkMessageID = msgID
	}
	oc.loggerForContext(ctx).Info().Stringer("event_id", eventID).Str("turn_id", turnID).Msg("Initial streaming message sent")
	return eventID
}

// flushPartialStreamingMessage saves the partially accumulated assistant message on context cancellation.
// This ensures that content already streamed to Matrix is persisted in the database.
func (oc *AIClient) flushPartialStreamingMessage(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if state == nil || state.initialEventID == "" || state.accumulated.Len() == 0 {
		return
	}
	state.completedAtMs = time.Now().UnixMilli()
	if !state.suppressSave {
		log := *oc.loggerForContext(ctx)
		log.Info().
			Str("event_id", state.initialEventID.String()).
			Int("accumulated_len", state.accumulated.Len()).
			Msg("Flushing partial streaming message on cancellation")
		oc.saveAssistantMessage(ctx, log, portal, state, meta)
	}
}

// sendFinalAssistantTurn sends an edit event with the complete assistant turn data.
// It processes response directives (reply tags, silent replies) before sending when in natural mode.
// Matches OpenClaw's directive processing behavior.
func (oc *AIClient) sendFinalAssistantTurn(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if portal == nil || portal.MXID == "" {
		return
	}
	if state != nil && state.heartbeat != nil {
		oc.sendFinalHeartbeatTurn(ctx, portal, state, meta)
		return
	}
	if state != nil && state.suppressSend {
		return
	}

	rawContent := state.accumulated.String()

	// Check response mode - simple mode skips directive processing
	responseMode := oc.getAgentResponseMode(meta)
	if responseMode == agents.ResponseModeSimple {
		// Simple mode: send content directly without directive processing
		cleanedRaw := airuntime.SanitizeChatMessageForDisplay(rawContent, false)
		rendered := format.RenderMarkdown(cleanedRaw, true, true)
		replyTo := id.EventID("")
		if state != nil {
			replyTo = state.replyTarget.EffectiveReplyTo()
		}
		if replyTo != "" {
			oc.sendFinalAssistantTurnContent(ctx, portal, state, meta, rendered, &replyTo)
		} else {
			oc.sendFinalAssistantTurnContent(ctx, portal, state, meta, rendered, nil)
		}
		return
	}

	// Natural mode: process directives (OpenClaw-style)
	directives := airuntime.ParseReplyDirectives(rawContent, state.sourceEventID.String())

	// Handle silent replies - redact the streaming message
	if directives.IsSilent {
		oc.loggerForContext(ctx).Debug().
			Str("turn_id", state.turnID).
			Str("initial_event_id", state.initialEventID.String()).
			Msg("Silent reply detected, redacting streaming message")
		oc.redactInitialStreamingMessage(ctx, portal, state)
		return
	}

	// Use cleaned content (directives stripped)
	cleanedContent := airuntime.SanitizeChatMessageForDisplay(airuntime.StripMessageIDHintLines(directives.Text), false)

	finalReplyTarget := oc.resolveFinalReplyTarget(meta, state, &directives)
	responsePrefix := resolveResponsePrefixForReply(oc, &oc.connector.Config, meta)
	if responsePrefix != "" && strings.TrimSpace(cleanedContent) != "" {
		if !strings.HasPrefix(cleanedContent, responsePrefix) {
			cleanedContent = responsePrefix + " " + cleanedContent
		}
	}
	rendered := format.RenderMarkdown(cleanedContent, true, true)

	// Safety-split oversized responses into multiple Matrix events
	var continuationBody string
	if len(rendered.Body) > streamtransport.MaxMatrixEventBodyBytes {
		firstBody, rest := streamtransport.SplitAtMarkdownBoundary(rendered.Body, streamtransport.MaxMatrixEventBodyBytes)
		continuationBody = rest
		rendered = format.RenderMarkdown(firstBody, true, true)
	}

	// Build AI SDK UIMessage payload
	parts := msgconv.ContentParts("", strings.TrimSpace(state.reasoning.String()))
	if cleanedContent != "" {
		parts = append(parts, map[string]any{
			"type":  "text",
			"text":  "", // omitted: full text is in m.new_content.body
			"state": "done",
		})
	}
	if toolParts := msgconv.ToolCallParts(state.toolCalls, string(ToolTypeProvider), string(ResultStatusSuccess), string(ResultStatusDenied)); len(toolParts) > 0 {
		parts = append(parts, toolParts...)
	}

	// Generate link previews for URLs in the response (needs intent for image upload)
	var linkPreviews []*event.BeeperLinkPreview
	if intent, err := oc.getIntentForPortal(ctx, portal, bridgev2.RemoteEventMessage); err == nil {
		linkPreviews = generateOutboundLinkPreviews(ctx, cleanedContent, intent, portal, state.sourceCitations, getLinkPreviewConfig(&oc.connector.Config))
	}

	uiMessage := msgconv.BuildUIMessage(msgconv.UIMessageParams{
		TurnID:     state.turnID,
		Role:       "assistant",
		Metadata:   oc.buildUIMessageMetadata(state, meta, true),
		Parts:      parts,
		SourceURLs: buildSourceParts(state.sourceCitations, state.sourceDocuments, linkPreviews),
		FileParts:  citations.GeneratedFilesToParts(state.generatedFiles),
	})

	topLevelExtra := map[string]any{
		BeeperAIKey:                     uiMessage,
		"com.beeper.dont_render_edited": true,
		"m.mentions":                    map[string]any{},
	}
	if lps := PreviewsToMapSlice(linkPreviews); len(lps) > 0 {
		topLevelExtra["com.beeper.linkpreviews"] = lps
	}

	sender := oc.senderForPortal(ctx, portal)
	oc.UserLogin.QueueRemoteEvent(&AIRemoteEdit{
		portal:        portal.PortalKey,
		sender:        sender,
		targetMessage: state.networkMessageID,
		timestamp:     time.Now(),
		preBuilt: &bridgev2.ConvertedEdit{
			ModifiedParts: []*bridgev2.ConvertedEditPart{{
				Type: event.EventMessage,
				Content: &event.MessageEventContent{
					MsgType:       event.MsgText,
					Body:          rendered.Body,
					Format:        rendered.Format,
					FormattedBody: rendered.FormattedBody,
				},
				Extra:         map[string]any{"m.mentions": map[string]any{}},
				TopLevelExtra: topLevelExtra,
			}},
		},
	})
	oc.recordAgentActivity(ctx, portal, meta)
	oc.loggerForContext(ctx).Debug().
		Str("initial_event_id", state.initialEventID.String()).
		Str("turn_id", state.turnID).
		Bool("has_thinking", state.reasoning.Len() > 0).
		Int("tool_calls", len(state.toolCalls)).
		Bool("has_reply", finalReplyTarget.ReplyTo != "").
		Int("link_previews", len(linkPreviews)).
		Msg("Queued final assistant turn edit")

	// Send continuation messages for overflow
	for continuationBody != "" {
		var chunk string
		chunk, continuationBody = streamtransport.SplitAtMarkdownBoundary(continuationBody, streamtransport.MaxMatrixEventBodyBytes)
		oc.sendContinuationMessage(ctx, portal, chunk)
	}
}

// sendFinalHeartbeatTurn handles heartbeat-specific response delivery.
func (oc *AIClient) sendFinalHeartbeatTurn(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if portal == nil || portal.MXID == "" || state == nil || state.heartbeat == nil {
		return
	}

	hb := state.heartbeat
	durationMs := time.Now().UnixMilli() - state.startedAtMs
	storeRef := sessionStoreRef{AgentID: hb.StoreAgentID, Path: hb.StorePath}
	rawContent := state.accumulated.String()
	ackMax := hb.AckMaxChars
	if ackMax < 0 {
		ackMax = agents.DefaultMaxAckChars
	}

	shouldSkip, strippedText, didStrip := agents.StripHeartbeatTokenWithMode(
		rawContent,
		agents.StripHeartbeatModeHeartbeat,
		ackMax,
	)
	finalText := rawContent
	if didStrip {
		finalText = strippedText
	}
	if hb.ExecEvent && strings.TrimSpace(rawContent) != "" {
		if strings.TrimSpace(finalText) == "" {
			finalText = rawContent
		}
		shouldSkip = false
	}
	responsePrefix := strings.TrimSpace(hb.ResponsePrefix)
	if responsePrefix != "" && strings.TrimSpace(finalText) != "" && !shouldSkip {
		if !strings.HasPrefix(finalText, responsePrefix) {
			finalText = responsePrefix + " " + finalText
		}
	}
	cleaned := strings.TrimSpace(finalText)
	hasMedia := len(state.pendingImages) > 0
	shouldSkipMain := shouldSkip && !hasMedia && !hb.ExecEvent
	hasContent := cleaned != ""
	includeReasoning := hb.IncludeReasoning && state.reasoning.Len() > 0
	reasoningText := ""
	if includeReasoning {
		reasoningText = strings.TrimSpace(state.reasoning.String())
		if reasoningText != "" {
			reasoningText = "Reasoning: " + reasoningText
		}
	}
	hasReasoning := reasoningText != ""
	deliverable := hb.TargetRoom != "" && hb.TargetRoom == portal.MXID
	targetReason := strings.TrimSpace(hb.TargetReason)
	if targetReason == "" {
		targetReason = "no-target"
	}

	sendOutcome := func(out HeartbeatRunOutcome) {
		if state.heartbeatResultCh != nil {
			select {
			case state.heartbeatResultCh <- out:
			default:
			}
		}
	}

	if shouldSkipMain && !hasContent && !hasReasoning {
		oc.restoreHeartbeatUpdatedAt(storeRef, hb.SessionKey, hb.PrevUpdatedAt)
		silent := true
		if hb.ShowOk && deliverable {
			heartbeatOk := agents.HeartbeatToken
			if responsePrefix != "" {
				heartbeatOk = responsePrefix + " " + agents.HeartbeatToken
			}
			oc.sendPlainAssistantMessage(ctx, portal, heartbeatOk)
			silent = false
		}
		oc.redactInitialStreamingMessage(ctx, portal, state)
		status := "ok-token"
		if strings.TrimSpace(rawContent) == "" {
			status = "ok-empty"
		}
		indicator := (*HeartbeatIndicatorType)(nil)
		if hb.UseIndicator {
			indicator = resolveIndicatorType(status)
		}
		oc.emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:            time.Now().UnixMilli(),
			Status:        status,
			To:            hb.TargetRoom.String(),
			Reason:        hb.Reason,
			Channel:       hb.Channel,
			Silent:        silent,
			HasMedia:      hasMedia,
			DurationMs:    durationMs,
			IndicatorType: indicator,
		})
		sendOutcome(HeartbeatRunOutcome{Status: "ran", Reason: status, Silent: silent, Skipped: true})
		return
	}

	// Deduplicate identical heartbeat content within 24h
	if hasContent && !shouldSkipMain && !hasMedia {
		if oc.isDuplicateHeartbeat(storeRef, hb.SessionKey, cleaned, state.startedAtMs) {
			oc.restoreHeartbeatUpdatedAt(storeRef, hb.SessionKey, hb.PrevUpdatedAt)
			oc.redactInitialStreamingMessage(ctx, portal, state)
			state.pendingImages = nil
			indicator := (*HeartbeatIndicatorType)(nil)
			if hb.UseIndicator {
				indicator = resolveIndicatorType("skipped")
			}
			oc.emitHeartbeatEvent(&HeartbeatEventPayload{
				TS:            time.Now().UnixMilli(),
				Status:        "skipped",
				Reason:        "duplicate",
				Preview:       cleaned[:min(len(cleaned), 200)],
				Channel:       hb.Channel,
				HasMedia:      hasMedia,
				DurationMs:    durationMs,
				IndicatorType: indicator,
			})
			sendOutcome(HeartbeatRunOutcome{Status: "ran", Reason: "duplicate", Skipped: true})
			return
		}
	}

	if !deliverable {
		oc.redactInitialStreamingMessage(ctx, portal, state)
		state.pendingImages = nil
		preview := cleaned
		if preview == "" && hasReasoning {
			preview = reasoningText
		}
		oc.emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:         time.Now().UnixMilli(),
			Status:     "skipped",
			Reason:     targetReason,
			To:         hb.TargetRoom.String(),
			Preview:    preview[:min(len(preview), 200)],
			Channel:    hb.Channel,
			HasMedia:   hasMedia,
			DurationMs: durationMs,
		})
		sendOutcome(HeartbeatRunOutcome{Status: "ran", Reason: targetReason, Skipped: true})
		return
	}

	if !hb.ShowAlerts {
		oc.restoreHeartbeatUpdatedAt(storeRef, hb.SessionKey, hb.PrevUpdatedAt)
		oc.redactInitialStreamingMessage(ctx, portal, state)
		state.pendingImages = nil
		indicator := (*HeartbeatIndicatorType)(nil)
		if hb.UseIndicator {
			indicator = resolveIndicatorType("sent")
		}
		preview := cleaned
		if preview == "" && hasReasoning {
			preview = reasoningText
		}
		oc.emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:            time.Now().UnixMilli(),
			Status:        "skipped",
			Reason:        "alerts-disabled",
			To:            hb.TargetRoom.String(),
			Preview:       preview[:min(len(preview), 200)],
			Channel:       hb.Channel,
			HasMedia:      hasMedia,
			DurationMs:    durationMs,
			IndicatorType: indicator,
		})
		sendOutcome(HeartbeatRunOutcome{Status: "ran", Reason: "alerts-disabled", Skipped: true})
		return
	}

	if hasReasoning {
		oc.sendPlainAssistantMessage(ctx, portal, reasoningText)
	}

	if cleaned != "" {
		if state.initialEventID == "" {
			oc.sendPlainAssistantMessage(ctx, portal, cleaned)
		} else {
			rendered := format.RenderMarkdown(cleaned, true, true)
			oc.sendFinalAssistantTurnContent(ctx, portal, state, meta, rendered, nil)
		}
	}

	// Record heartbeat for dedupe
	if hb.SessionKey != "" && cleaned != "" && !shouldSkipMain {
		oc.recordHeartbeatText(storeRef, hb.SessionKey, cleaned, state.startedAtMs)
	}

	indicator := (*HeartbeatIndicatorType)(nil)
	if hb.UseIndicator {
		indicator = resolveIndicatorType("sent")
	}
	preview := cleaned
	if preview == "" && hasReasoning {
		preview = reasoningText
	}
	oc.emitHeartbeatEvent(&HeartbeatEventPayload{
		TS:            time.Now().UnixMilli(),
		Status:        "sent",
		To:            hb.TargetRoom.String(),
		Reason:        hb.Reason,
		Preview:       preview[:min(len(preview), 200)],
		Channel:       hb.Channel,
		HasMedia:      hasMedia,
		DurationMs:    durationMs,
		IndicatorType: indicator,
	})
	sendOutcome(HeartbeatRunOutcome{Status: "ran", Text: cleaned, Sent: true})
}

func (oc *AIClient) redactInitialStreamingMessage(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if portal == nil || state == nil {
		return
	}
	if state.initialEventID == "" {
		return
	}
	if state.networkMessageID != "" {
		if err := oc.redactViaPortal(ctx, portal, state.networkMessageID); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", state.initialEventID).Msg("Failed to redact streaming message via network ID")
		}
		return
	}
	if err := oc.redactEventViaPortal(ctx, portal, state.initialEventID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", state.initialEventID).Msg("Failed to redact streaming message via event ID")
	}
}

func (oc *AIClient) sendPlainAssistantMessage(ctx context.Context, portal *bridgev2.Portal, text string) {
	if portal == nil || portal.MXID == "" {
		return
	}
	meta := portalMeta(portal)
	agentID := resolveAgentID(meta)
	modelID := oc.effectiveModel(meta)
	msg := NewAITextMessage(portal, oc.UserLogin, text, meta, agentID, modelID)
	oc.UserLogin.QueueRemoteEvent(msg)
	oc.recordAgentActivity(ctx, portal, meta)
}

// sendPlainAssistantMessageWithResult is used by automated delivery paths where failures should be
// observable by the caller (e.g. so a background runner doesn't get stuck on a blocked send forever).
func (oc *AIClient) sendPlainAssistantMessageWithResult(ctx context.Context, portal *bridgev2.Portal, text string) error {
	if portal == nil || portal.MXID == "" {
		return nil
	}

	rendered := format.RenderMarkdown(text, true, true)
	eventRaw := map[string]any{
		"msgtype":        event.MsgText,
		"body":           rendered.Body,
		"format":         rendered.Format,
		"formatted_body": rendered.FormattedBody,
		"m.mentions":     map[string]any{},
	}

	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:      networkid.PartID("0"),
			Type:    event.EventMessage,
			Content: &event.MessageEventContent{MsgType: event.MsgText, Body: rendered.Body},
			Extra:   eventRaw,
		}},
	}

	if _, _, err := oc.sendViaPortal(ctx, portal, converted, ""); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("room_id", portal.MXID).Msg("Failed to send plain assistant message")
		return err
	}
	oc.recordAgentActivity(ctx, portal, portalMeta(portal))
	return nil
}

func buildSourceParts(cits []citations.SourceCitation, documents []citations.SourceDocument, previews []*event.BeeperLinkPreview) []map[string]any {
	if len(cits) == 0 && len(documents) == 0 && len(previews) == 0 {
		return nil
	}

	// Build a preview-by-URL index so we can enrich citation metadata with
	// uploaded image URIs and dimensions from link previews.
	previewByURL := make(map[string]*event.BeeperLinkPreview, len(previews))
	for _, p := range previews {
		if p == nil {
			continue
		}
		for _, u := range []string{p.MatchedURL, p.CanonicalURL} {
			u = strings.TrimSpace(u)
			if u != "" {
				if _, exists := previewByURL[u]; !exists {
					previewByURL[u] = p
				}
			}
		}
	}

	parts := make([]map[string]any, 0, len(cits)+len(documents)+len(previews))
	seen := make(map[string]struct{}, len(cits)+len(documents)+len(previews))

	appendURL := func(url, title string, providerMetadata map[string]any) {
		url = strings.TrimSpace(url)
		if url == "" {
			return
		}
		seenKey := "url:" + url
		if _, ok := seen[seenKey]; ok {
			return
		}
		seen[seenKey] = struct{}{}

		part := map[string]any{
			"type":     "source-url",
			"sourceId": fmt.Sprintf("source-%d", len(parts)+1),
			"url":      url,
		}
		if title = strings.TrimSpace(title); title != "" {
			part["title"] = title
		}
		if len(providerMetadata) > 0 {
			part["providerMetadata"] = providerMetadata
		}
		parts = append(parts, part)
	}

	for _, citation := range cits {
		meta := citations.ProviderMetadata(citation)

		// Enrich with uploaded image URI and dimensions from the matching link preview.
		if p := previewByURL[strings.TrimSpace(citation.URL)]; p != nil {
			if meta == nil {
				meta = map[string]any{}
			}
			if p.ImageURL != "" {
				meta["image_url"] = string(p.ImageURL)
			}
			if p.ImageWidth != 0 {
				meta["image_width"] = int(p.ImageWidth)
			}
			if p.ImageHeight != 0 {
				meta["image_height"] = int(p.ImageHeight)
			}
		}

		appendURL(citation.URL, citation.Title, meta)
	}

	for _, doc := range documents {
		key := strings.TrimSpace(doc.ID)
		if key == "" {
			key = strings.TrimSpace(doc.Filename)
		}
		if key == "" {
			key = strings.TrimSpace(doc.Title)
		}
		if key == "" {
			continue
		}
		seenKey := "doc:" + key
		if _, ok := seen[seenKey]; ok {
			continue
		}
		seen[seenKey] = struct{}{}
		part := map[string]any{
			"type":      "source-document",
			"sourceId":  fmt.Sprintf("source-%d", len(parts)+1),
			"mediaType": doc.MediaType,
			"title":     doc.Title,
		}
		if filename := strings.TrimSpace(doc.Filename); filename != "" {
			part["filename"] = filename
		}
		parts = append(parts, part)
	}

	for _, preview := range previews {
		if preview == nil {
			continue
		}
		url := strings.TrimSpace(preview.CanonicalURL)
		if url == "" {
			url = strings.TrimSpace(preview.MatchedURL)
		}
		if url == "" {
			continue
		}
		title := strings.TrimSpace(preview.Title)
		if title == "" {
			title = strings.TrimSpace(preview.SiteName)
		}
		meta := map[string]any{}
		if desc := strings.TrimSpace(preview.Description); desc != "" {
			meta["description"] = desc
		}
		if site := strings.TrimSpace(preview.SiteName); site != "" {
			meta["site_name"] = site
		}
		if len(meta) == 0 {
			meta = nil
		}
		appendURL(url, title, meta)
	}

	return parts
}

// sendFinalAssistantTurnContent is a helper for simple mode that sends content without directive processing.
func (oc *AIClient) sendFinalAssistantTurnContent(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata, rendered event.MessageEventContent, replyToEventID *id.EventID) {
	// Safety-split oversized responses into multiple Matrix events
	var continuationBody string
	if len(rendered.Body) > streamtransport.MaxMatrixEventBodyBytes {
		firstBody, rest := streamtransport.SplitAtMarkdownBoundary(rendered.Body, streamtransport.MaxMatrixEventBodyBytes)
		continuationBody = rest
		rendered = format.RenderMarkdown(firstBody, true, true)
	}

	// Build AI metadata
	parts := msgconv.ContentParts("", strings.TrimSpace(state.reasoning.String()))
	if rendered.Body != "" {
		parts = append(parts, map[string]any{
			"type":  "text",
			"text":  "", // omitted: full text is in m.new_content.body
			"state": "done",
		})
	}
	if toolParts := msgconv.ToolCallParts(state.toolCalls, string(ToolTypeProvider), string(ResultStatusSuccess), string(ResultStatusDenied)); len(toolParts) > 0 {
		parts = append(parts, toolParts...)
	}

	replyTo := id.EventID("")
	if replyToEventID != nil {
		replyTo = *replyToEventID
	}
	relatesTo := msgconv.RelatesToReplace(state.initialEventID, replyTo)

	// Generate link previews for URLs in the response
	intent, _ := oc.getIntentForPortal(ctx, portal, bridgev2.RemoteEventMessage)
	linkPreviews := generateOutboundLinkPreviews(ctx, rendered.Body, intent, portal, state.sourceCitations, getLinkPreviewConfig(&oc.connector.Config))

	uiMessage := msgconv.BuildUIMessage(msgconv.UIMessageParams{
		TurnID:     state.turnID,
		Role:       "assistant",
		Metadata:   oc.buildUIMessageMetadata(state, meta, true),
		Parts:      parts,
		SourceURLs: buildSourceParts(state.sourceCitations, state.sourceDocuments, linkPreviews),
		FileParts:  citations.GeneratedFilesToParts(state.generatedFiles),
	})

	eventContent := msgconv.BuildFinalEditContent(msgconv.FinalEditContentParams{
		Rendered:       rendered,
		RelatesTo:      relatesTo,
		UIMessage:      uiMessage,
		LinkPreviews:   PreviewsToMapSlice(linkPreviews),
		DontShowEdited: true,
	})

	sender := oc.senderForPortal(ctx, portal)
	editContent := &bridgev2.ConvertedEdit{
		ModifiedParts: []*bridgev2.ConvertedEditPart{{
			Part:    nil,
			Type:    event.EventMessage,
			Content: &event.MessageEventContent{MsgType: event.MsgText, Body: rendered.Body},
			Extra:   eventContent.Raw,
			TopLevelExtra: map[string]any{
				"m.new_content": eventContent.Raw,
			},
		}},
	}
	oc.UserLogin.QueueRemoteEvent(&AIRemoteEdit{
		portal:        portal.PortalKey,
		sender:        sender,
		targetMessage: state.networkMessageID,
		preBuilt:      editContent,
	})
	oc.recordAgentActivity(ctx, portal, meta)
	oc.loggerForContext(ctx).Debug().
		Str("initial_event_id", state.initialEventID.String()).
		Str("turn_id", state.turnID).
		Str("mode", "simple").
		Int("link_previews", len(linkPreviews)).
		Msg("Queued final assistant turn edit (simple mode)")

	// Send continuation messages for overflow
	for continuationBody != "" {
		var chunk string
		chunk, continuationBody = streamtransport.SplitAtMarkdownBoundary(continuationBody, streamtransport.MaxMatrixEventBodyBytes)
		oc.sendContinuationMessage(ctx, portal, chunk)
	}
}

// generateOutboundLinkPreviews extracts URLs from AI response text, generates link previews, and uploads images to Matrix.
// When citations are provided (e.g. from Exa search results), matching URLs use the citation's
// image directly instead of fetching the page's HTML.
func generateOutboundLinkPreviews(ctx context.Context, text string, intent bridgev2.MatrixAPI, portal *bridgev2.Portal, cits []citations.SourceCitation, config LinkPreviewConfig) []*event.BeeperLinkPreview {
	if !config.Enabled {
		return nil
	}

	urls := ExtractURLs(text, config.MaxURLsOutbound)
	if len(urls) == 0 {
		return nil
	}

	previewer := NewLinkPreviewer(config)
	fetchCtx, cancel := context.WithTimeout(ctx, config.FetchTimeout*time.Duration(len(urls)))
	defer cancel()

	var previewsWithImages []*PreviewWithImage
	if len(cits) > 0 {
		previewsWithImages = previewer.FetchPreviewsWithCitations(fetchCtx, urls, cits)
	} else {
		previewsWithImages = previewer.FetchPreviews(fetchCtx, urls)
	}

	// Upload images to Matrix and get final previews
	return UploadPreviewImages(ctx, previewsWithImages, intent, portal.MXID)
}

// getAgentResponseMode returns the response mode for the current agent.
// Defaults to ResponseModeNatural if not set.
// IsSimpleMode on the portal overrides all other settings (for simple mode rooms).
func (oc *AIClient) getAgentResponseMode(meta *PortalMetadata) agents.ResponseMode {
	// Simple mode flag takes priority (set by simple command)
	if isSimpleMode(meta) {
		return agents.ResponseModeSimple
	}

	agentID := resolveAgentID(meta)

	if agentID != "" {
		store := NewAgentStoreAdapter(oc)
		if agent, err := store.GetAgentByID(context.Background(), agentID); err == nil && agent != nil {
			if agent.ResponseMode != "" {
				return agent.ResponseMode
			}
		}
	}

	// Default to natural mode (OpenClaw-style)
	return agents.ResponseModeNatural
}
