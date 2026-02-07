package connector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/agents"
)

// sendInitialStreamMessage sends the first message in a streaming session and returns its event ID
func (oc *AIClient) sendInitialStreamMessage(ctx context.Context, portal *bridgev2.Portal, content string, turnID string, replyTarget ReplyTarget) id.EventID {
	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return ""
	}

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

	eventContent := &event.Content{
		Raw: map[string]any{
			"msgtype":      event.MsgText,
			"body":         content,
			"m.relates_to": relatesTo,
			BeeperAIKey:    uiMessage,
		},
	}
	resp, err := intent.SendMessage(ctx, portal.MXID, event.EventMessage, eventContent, nil)
	if err != nil {
		oc.loggerForContext(ctx).Error().Err(err).Msg("Failed to send initial streaming message")
		return ""
	}
	oc.loggerForContext(ctx).Info().Stringer("event_id", resp.EventID).Str("turn_id", turnID).Msg("Initial streaming message sent")
	return resp.EventID
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
	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return
	}

	rawContent := state.accumulated.String()

	// Check response mode - raw mode skips directive processing
	responseMode := oc.getAgentResponseMode(meta)
	if responseMode == agents.ResponseModeRaw {
		// Raw mode: send content directly without directive processing
		rendered := format.RenderMarkdown(rawContent, true, true)
		replyTo := id.EventID("")
		if state != nil {
			replyTo = state.replyTarget.EffectiveReplyTo()
		}
		if replyTo != "" {
			oc.sendFinalAssistantTurnContent(ctx, portal, state, meta, intent, rendered, &replyTo)
		} else {
			oc.sendFinalAssistantTurnContent(ctx, portal, state, meta, intent, rendered, nil)
		}
		return
	}

	// Natural mode: process directives (OpenClaw-style)
	directives := ParseResponseDirectives(rawContent, state.sourceEventID)

	// Handle silent replies - redact the streaming message
	if directives.IsSilent {
		oc.loggerForContext(ctx).Debug().
			Str("turn_id", state.turnID).
			Str("initial_event_id", state.initialEventID.String()).
			Msg("Silent reply detected, redacting streaming message")

		// Redact the initial streaming message
		if state.initialEventID != "" {
			_, err := intent.SendMessage(ctx, portal.MXID, event.EventRedaction, &event.Content{
				Parsed: &event.RedactionEventContent{
					Redacts: state.initialEventID,
				},
			}, nil)
			if err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", state.initialEventID).Msg("Failed to redact silent reply message")
			}
		}
		return
	}

	// Use cleaned content (directives stripped)
	cleanedContent := stripMessageIDHintLines(directives.Text)

	finalReplyTarget := oc.resolveFinalReplyTarget(meta, state, directives)
	responsePrefix := resolveResponsePrefixForReply(oc, &oc.connector.Config, meta)
	if responsePrefix != "" && strings.TrimSpace(cleanedContent) != "" {
		if !strings.HasPrefix(cleanedContent, responsePrefix) {
			cleanedContent = responsePrefix + " " + cleanedContent
		}
	}
	rendered := format.RenderMarkdown(cleanedContent, true, true)

	// Build AI SDK UIMessage payload
	parts := make([]map[string]any, 0, 2+len(state.toolCalls))
	if state.reasoning.Len() > 0 {
		parts = append(parts, map[string]any{
			"type":  "reasoning",
			"text":  state.reasoning.String(),
			"state": "done",
		})
	}
	if cleanedContent != "" {
		parts = append(parts, map[string]any{
			"type":  "text",
			"text":  cleanedContent,
			"state": "done",
		})
	}
	for _, tc := range state.toolCalls {
		toolPart := map[string]any{
			"type":       "dynamic-tool",
			"toolName":   tc.ToolName,
			"toolCallId": tc.CallID,
			"input":      tc.Input,
		}
		if tc.ToolType == string(ToolTypeProvider) {
			toolPart["providerExecuted"] = true
		}
		if tc.ResultStatus == string(ResultStatusSuccess) {
			toolPart["state"] = "output-available"
			toolPart["output"] = tc.Output
		} else {
			toolPart["state"] = "output-error"
			if tc.ErrorMessage != "" {
				toolPart["errorText"] = tc.ErrorMessage
			} else if result, ok := tc.Output["result"].(string); ok && result != "" {
				toolPart["errorText"] = result
			}
		}
		parts = append(parts, toolPart)
	}

	// Build m.relates_to with replace relation
	relatesTo := map[string]any{
		"rel_type": RelReplace,
		"event_id": state.initialEventID.String(),
	}

	// Add reply relation if enabled
	if finalReplyTarget.EffectiveReplyTo() != "" {
		relatesTo["m.in_reply_to"] = map[string]any{
			"event_id": finalReplyTarget.EffectiveReplyTo().String(),
		}
	}

	// Generate link previews for URLs in the response
	linkPreviews := oc.generateOutboundLinkPreviews(ctx, cleanedContent, intent, portal)
	if sourceParts := buildSourceParts(state.sourceCitations, state.sourceDocuments, linkPreviews); len(sourceParts) > 0 {
		parts = append(parts, sourceParts...)
	}
	if fileParts := generatedFilesToParts(state.generatedFiles); len(fileParts) > 0 {
		parts = append(parts, fileParts...)
	}

	uiMessage := map[string]any{
		"id":       state.turnID,
		"role":     "assistant",
		"metadata": oc.buildUIMessageMetadata(state, meta, true),
		"parts":    parts,
	}

	// Send edit event with m.replace relation and m.new_content
	eventRawContent := map[string]any{
		"msgtype":        event.MsgText,
		"body":           "* " + rendered.Body, // Fallback with edit marker
		"format":         rendered.Format,
		"formatted_body": "* " + rendered.FormattedBody,
		"m.new_content": map[string]any{
			"msgtype":        event.MsgText,
			"body":           rendered.Body,
			"format":         rendered.Format,
			"formatted_body": rendered.FormattedBody,
		},
		"m.relates_to":                  relatesTo,
		BeeperAIKey:                     uiMessage,
		"com.beeper.dont_render_edited": true, // Don't show "edited" indicator for streaming updates
	}

	// Attach link previews if any were generated
	if len(linkPreviews) > 0 {
		eventRawContent["com.beeper.linkpreviews"] = PreviewsToMapSlice(linkPreviews)
	}

	eventContent := &event.Content{Raw: eventRawContent}

	if _, err := intent.SendMessage(ctx, portal.MXID, event.EventMessage, eventContent, nil); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("initial_event_id", state.initialEventID).Msg("Failed to send final assistant turn")
	} else {
		oc.recordAgentActivity(ctx, portal, meta)
		oc.loggerForContext(ctx).Debug().
			Str("initial_event_id", state.initialEventID.String()).
			Str("turn_id", state.turnID).
			Bool("has_thinking", state.reasoning.Len() > 0).
			Int("tool_calls", len(state.toolCalls)).
			Bool("has_reply", directives.ReplyToEventID != "").
			Int("link_previews", len(linkPreviews)).
			Msg("Sent final assistant turn with metadata")
	}
}

// sendFinalHeartbeatTurn handles heartbeat-specific response delivery.
func (oc *AIClient) sendFinalHeartbeatTurn(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if portal == nil || portal.MXID == "" || state == nil || state.heartbeat == nil {
		return
	}
	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return
	}

	hb := state.heartbeat
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
		oc.redactInitialStreamingMessage(ctx, portal, intent, state)
		status := "ok-token"
		if strings.TrimSpace(rawContent) == "" {
			status = "ok-empty"
		}
		indicator := (*HeartbeatIndicatorType)(nil)
		if hb.UseIndicator {
			indicator = resolveIndicatorType(status)
		}
		emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:            time.Now().UnixMilli(),
			Status:        status,
			Reason:        hb.Reason,
			Channel:       hb.Channel,
			Silent:        silent,
			HasMedia:      hasMedia,
			IndicatorType: indicator,
		})
		sendOutcome(HeartbeatRunOutcome{Status: "ran", Reason: status, Silent: silent, Skipped: true})
		return
	}

	// Deduplicate identical heartbeat content within 24h
	if hasContent && !shouldSkipMain && !hasMedia {
		if oc.isDuplicateHeartbeat(storeRef, hb.SessionKey, cleaned, state.startedAtMs) {
			oc.restoreHeartbeatUpdatedAt(storeRef, hb.SessionKey, hb.PrevUpdatedAt)
			oc.redactInitialStreamingMessage(ctx, portal, intent, state)
			state.pendingImages = nil
			indicator := (*HeartbeatIndicatorType)(nil)
			if hb.UseIndicator {
				indicator = resolveIndicatorType("skipped")
			}
			emitHeartbeatEvent(&HeartbeatEventPayload{
				TS:            time.Now().UnixMilli(),
				Status:        "skipped",
				Reason:        "duplicate",
				Preview:       cleaned[:minInt(len(cleaned), 200)],
				Channel:       hb.Channel,
				HasMedia:      hasMedia,
				IndicatorType: indicator,
			})
			sendOutcome(HeartbeatRunOutcome{Status: "ran", Reason: "duplicate", Skipped: true})
			return
		}
	}

	if !deliverable {
		oc.redactInitialStreamingMessage(ctx, portal, intent, state)
		state.pendingImages = nil
		preview := cleaned
		if preview == "" && hasReasoning {
			preview = reasoningText
		}
		emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:       time.Now().UnixMilli(),
			Status:   "skipped",
			Reason:   targetReason,
			Preview:  preview[:minInt(len(preview), 200)],
			Channel:  hb.Channel,
			HasMedia: hasMedia,
		})
		sendOutcome(HeartbeatRunOutcome{Status: "ran", Reason: targetReason, Skipped: true})
		return
	}

	if !hb.ShowAlerts {
		oc.restoreHeartbeatUpdatedAt(storeRef, hb.SessionKey, hb.PrevUpdatedAt)
		oc.redactInitialStreamingMessage(ctx, portal, intent, state)
		state.pendingImages = nil
		indicator := (*HeartbeatIndicatorType)(nil)
		if hb.UseIndicator {
			indicator = resolveIndicatorType("sent")
		}
		preview := cleaned
		if preview == "" && hasReasoning {
			preview = reasoningText
		}
		emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:            time.Now().UnixMilli(),
			Status:        "skipped",
			Reason:        "alerts-disabled",
			Preview:       preview[:minInt(len(preview), 200)],
			Channel:       hb.Channel,
			HasMedia:      hasMedia,
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
			oc.sendFinalAssistantTurnContent(ctx, portal, state, meta, intent, rendered, nil)
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
	emitHeartbeatEvent(&HeartbeatEventPayload{
		TS:            time.Now().UnixMilli(),
		Status:        "sent",
		Reason:        hb.Reason,
		Preview:       preview[:minInt(len(preview), 200)],
		Channel:       hb.Channel,
		HasMedia:      hasMedia,
		IndicatorType: indicator,
	})
	sendOutcome(HeartbeatRunOutcome{Status: "ran", Text: cleaned, Sent: true})
}

func (oc *AIClient) redactInitialStreamingMessage(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, state *streamingState) {
	if portal == nil || intent == nil || state == nil {
		return
	}
	if state.initialEventID == "" {
		return
	}
	_, err := intent.SendMessage(ctx, portal.MXID, event.EventRedaction, &event.Content{
		Parsed: &event.RedactionEventContent{
			Redacts: state.initialEventID,
		},
	}, nil)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", state.initialEventID).Msg("Failed to redact heartbeat reply message")
	}
}

func (oc *AIClient) sendPlainAssistantMessage(ctx context.Context, portal *bridgev2.Portal, text string) {
	if portal == nil || portal.MXID == "" {
		return
	}
	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return
	}
	rendered := format.RenderMarkdown(text, true, true)
	eventRawContent := map[string]any{
		"msgtype":        event.MsgText,
		"body":           rendered.Body,
		"format":         rendered.Format,
		"formatted_body": rendered.FormattedBody,
	}
	if _, err := intent.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Raw: eventRawContent}, nil); err == nil {
		oc.recordAgentActivity(ctx, portal, portalMeta(portal))
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func buildSourceParts(citations []sourceCitation, documents []sourceDocument, previews []*event.BeeperLinkPreview) []map[string]any {
	if len(citations) == 0 && len(documents) == 0 && len(previews) == 0 {
		return nil
	}

	parts := make([]map[string]any, 0, len(citations)+len(documents)+len(previews))
	seen := make(map[string]struct{}, len(citations)+len(documents)+len(previews))

	appendURL := func(url, title string, providerMetadata map[string]any) {
		url = strings.TrimSpace(url)
		if url == "" {
			return
		}
		if _, ok := seen[url]; ok {
			return
		}
		seen[url] = struct{}{}

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

	for _, citation := range citations {
		meta := map[string]any{}
		if desc := strings.TrimSpace(citation.Description); desc != "" {
			meta["description"] = desc
		}
		if published := strings.TrimSpace(citation.Published); published != "" {
			meta["published"] = published
		}
		if site := strings.TrimSpace(citation.SiteName); site != "" {
			meta["site_name"] = site
		}
		if author := strings.TrimSpace(citation.Author); author != "" {
			meta["author"] = author
		}
		if image := strings.TrimSpace(citation.Image); image != "" {
			meta["image"] = image
		}
		if favicon := strings.TrimSpace(citation.Favicon); favicon != "" {
			meta["favicon"] = favicon
		}
		if len(meta) == 0 {
			meta = nil
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
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
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

func generatedFilesToParts(files []generatedFilePart) []map[string]any {
	if len(files) == 0 {
		return nil
	}
	parts := make([]map[string]any, 0, len(files))
	for _, file := range files {
		if strings.TrimSpace(file.url) == "" {
			continue
		}
		part := map[string]any{
			"type":      "file",
			"url":       file.url,
			"mediaType": strings.TrimSpace(file.mediaType),
		}
		parts = append(parts, part)
	}
	return parts
}

// sendFinalAssistantTurnContent is a helper for raw mode that sends content without directive processing.
func (oc *AIClient) sendFinalAssistantTurnContent(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata, intent bridgev2.MatrixAPI, rendered event.MessageEventContent, replyToEventID *id.EventID) {
	// Build AI metadata
	parts := make([]map[string]any, 0, 2+len(state.toolCalls))
	if state.reasoning.Len() > 0 {
		parts = append(parts, map[string]any{
			"type":  "reasoning",
			"text":  state.reasoning.String(),
			"state": "done",
		})
	}
	if rendered.Body != "" {
		parts = append(parts, map[string]any{
			"type":  "text",
			"text":  rendered.Body,
			"state": "done",
		})
	}
	for _, tc := range state.toolCalls {
		toolPart := map[string]any{
			"type":       "dynamic-tool",
			"toolName":   tc.ToolName,
			"toolCallId": tc.CallID,
			"input":      tc.Input,
		}
		if tc.ToolType == string(ToolTypeProvider) {
			toolPart["providerExecuted"] = true
		}
		if tc.ResultStatus == string(ResultStatusSuccess) {
			toolPart["state"] = "output-available"
			toolPart["output"] = tc.Output
		} else {
			toolPart["state"] = "output-error"
			if tc.ErrorMessage != "" {
				toolPart["errorText"] = tc.ErrorMessage
			} else if result, ok := tc.Output["result"].(string); ok && result != "" {
				toolPart["errorText"] = result
			}
		}
		parts = append(parts, toolPart)
	}

	relatesTo := map[string]any{
		"rel_type": RelReplace,
		"event_id": state.initialEventID.String(),
	}

	if replyToEventID != nil && *replyToEventID != "" {
		relatesTo["m.in_reply_to"] = map[string]any{
			"event_id": replyToEventID.String(),
		}
	}

	// Generate link previews for URLs in the response
	linkPreviews := oc.generateOutboundLinkPreviews(ctx, rendered.Body, intent, portal)
	if sourceParts := buildSourceParts(state.sourceCitations, state.sourceDocuments, linkPreviews); len(sourceParts) > 0 {
		parts = append(parts, sourceParts...)
	}
	if fileParts := generatedFilesToParts(state.generatedFiles); len(fileParts) > 0 {
		parts = append(parts, fileParts...)
	}

	uiMessage := map[string]any{
		"id":       state.turnID,
		"role":     "assistant",
		"metadata": oc.buildUIMessageMetadata(state, meta, true),
		"parts":    parts,
	}

	rawContent2 := map[string]any{
		"msgtype":                       event.MsgText,
		"body":                          "* " + rendered.Body,
		"format":                        rendered.Format,
		"formatted_body":                "* " + rendered.FormattedBody,
		"m.new_content":                 map[string]any{"msgtype": event.MsgText, "body": rendered.Body, "format": rendered.Format, "formatted_body": rendered.FormattedBody},
		"m.relates_to":                  relatesTo,
		BeeperAIKey:                     uiMessage,
		"com.beeper.dont_render_edited": true,
	}

	// Attach link previews if any were generated
	if len(linkPreviews) > 0 {
		rawContent2["com.beeper.linkpreviews"] = PreviewsToMapSlice(linkPreviews)
	}

	eventContent := &event.Content{Raw: rawContent2}

	if _, err := intent.SendMessage(ctx, portal.MXID, event.EventMessage, eventContent, nil); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("initial_event_id", state.initialEventID).Msg("Failed to send final assistant turn (raw mode)")
	} else {
		oc.recordAgentActivity(ctx, portal, meta)
		oc.loggerForContext(ctx).Debug().
			Str("initial_event_id", state.initialEventID.String()).
			Str("turn_id", state.turnID).
			Str("mode", "raw").
			Int("link_previews", len(linkPreviews)).
			Msg("Sent final assistant turn (raw mode)")
	}
}

// generateOutboundLinkPreviews extracts URLs from AI response text, generates link previews, and uploads images to Matrix.
func (oc *AIClient) generateOutboundLinkPreviews(ctx context.Context, text string, intent bridgev2.MatrixAPI, portal *bridgev2.Portal) []*event.BeeperLinkPreview {
	config := oc.getLinkPreviewConfig()
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

	previewsWithImages := previewer.FetchPreviews(fetchCtx, urls)

	// Upload images to Matrix and get final previews
	return UploadPreviewImages(ctx, previewsWithImages, intent, portal.MXID)
}

// getAgentResponseMode returns the response mode for the current agent.
// Defaults to ResponseModeNatural if not set.
// IsRawMode on the portal overrides all other settings (for playground rooms).
func (oc *AIClient) getAgentResponseMode(meta *PortalMetadata) agents.ResponseMode {
	// IsRawMode flag takes priority (set by playground command)
	if meta.IsRawMode {
		return agents.ResponseModeRaw
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
