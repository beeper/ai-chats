package connector

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/bridgeadapter"
	"github.com/beeper/agentremote/pkg/connector/msgconv"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/pkg/shared/streamtransport"
)

// sendContinuationMessage sends overflow text as a new (non-edit) message from the bot.
func (oc *AIClient) sendContinuationMessage(ctx context.Context, portal *bridgev2.Portal, body string) {
	if portal == nil || portal.MXID == "" {
		return
	}
	msg := bridgeadapter.BuildContinuationMessage(portal.PortalKey, body, oc.senderForPortal(ctx, portal), "ai", "ai_msg_id")
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
		"msgtype":    event.MsgText,
		"body":       content,
		BeeperAIKey:  uiMessage,
		"m.mentions": map[string]any{},
	}
	if relatesTo != nil {
		eventRaw["m.relates_to"] = relatesTo
	}

	msgID := bridgeadapter.NewMessageID("ai")
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:         networkid.PartID("0"),
			Type:       event.EventMessage,
			Content:    &event.MessageEventContent{MsgType: event.MsgText, Body: content},
			Extra:      eventRaw,
			DBMetadata: &MessageMetadata{BaseMessageMetadata: bridgeadapter.BaseMessageMetadata{Role: "assistant", TurnID: turnID}},
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
	if state == nil || !state.hasInitialMessageTarget() || state.accumulated.Len() == 0 {
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
		if strings.TrimSpace(cleanedRaw) == "" {
			cleanedRaw = finalRenderedBodyFallback(state)
		}
		rendered := format.RenderMarkdown(cleanedRaw, true, true)
		oc.sendFinalAssistantTurnContent(ctx, portal, state, meta, rendered, nil, "simple")
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
	cleanedContent := airuntime.SanitizeChatMessageForDisplay(directives.Text, false)
	if strings.TrimSpace(cleanedContent) == "" {
		cleanedContent = finalRenderedBodyFallback(state)
	}

	finalReplyTarget := oc.resolveFinalReplyTarget(meta, state, &directives)
	rendered := format.RenderMarkdown(cleanedContent, true, true)
	if finalReplyTarget.ReplyTo != "" {
		replyTo := finalReplyTarget.ReplyTo
		oc.sendFinalAssistantTurnContent(ctx, portal, state, meta, rendered, &replyTo, "natural")
	} else {
		oc.sendFinalAssistantTurnContent(ctx, portal, state, meta, rendered, nil, "natural")
	}
}

// heartbeatSkipParams captures the per-branch differences for the common
// heartbeat-skip path (redact, emit event, send outcome, return).
type heartbeatSkipParams struct {
	status    string // event payload status ("ok-token", "ok-empty", "skipped")
	reason    string // outcome & event reason
	restore   bool   // whether to restore heartbeat updatedAt
	indicator *HeartbeatIndicatorType
	preview   string // truncated to 200 chars
	to        string // target room string for the event
	silent    bool   // for the event payload & outcome
	sent      bool   // whether this branch emitted a visible message
}

// skipHeartbeatRun executes the common heartbeat-skip path shared by all early-
// return branches: optionally restore the heartbeat timestamp, redact the
// streaming message, clear pending images, emit the heartbeat event, send the
// outcome, and return.
func (oc *AIClient) skipHeartbeatRun(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	hb *HeartbeatRunConfig,
	durationMs int64,
	hasMedia bool,
	sendOutcome func(HeartbeatRunOutcome),
	p heartbeatSkipParams,
) {
	if p.restore {
		storeRef := sessionStoreRef{AgentID: hb.StoreAgentID}
		oc.restoreHeartbeatUpdatedAt(storeRef, hb.SessionKey, hb.PrevUpdatedAt)
	}
	oc.redactInitialStreamingMessage(ctx, portal, state)
	state.pendingImages = nil

	preview := p.preview
	if len(preview) > 200 {
		preview = preview[:200]
	}

	oc.emitHeartbeatEvent(&HeartbeatEventPayload{
		TS:            time.Now().UnixMilli(),
		Status:        p.status,
		To:            p.to,
		Reason:        p.reason,
		Preview:       preview,
		Channel:       hb.Channel,
		Silent:        p.silent,
		HasMedia:      hasMedia,
		DurationMs:    durationMs,
		IndicatorType: p.indicator,
	})
	sendOutcome(HeartbeatRunOutcome{
		Status:  "ran",
		Reason:  p.reason,
		Preview: preview,
		Sent:    p.sent,
		Silent:  p.silent,
		Skipped: true,
	})
}

// sendFinalHeartbeatTurn handles heartbeat-specific response delivery.
func (oc *AIClient) sendFinalHeartbeatTurn(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if portal == nil || portal.MXID == "" || state == nil || state.heartbeat == nil {
		return
	}

	hb := state.heartbeat
	durationMs := time.Now().UnixMilli() - state.startedAtMs
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

	// Helper to pick preview text, preferring cleaned content then reasoning.
	previewText := func() string {
		if cleaned != "" {
			return cleaned
		}
		if hasReasoning {
			return reasoningText
		}
		return ""
	}

	if shouldSkipMain && !hasContent && !hasReasoning {
		silent := true
		if hb.ShowOk && deliverable {
			oc.sendPlainAssistantMessage(ctx, portal, agents.HeartbeatToken)
			silent = false
		}
		status := "ok-token"
		if strings.TrimSpace(rawContent) == "" {
			status = "ok-empty"
		}
		var indicator *HeartbeatIndicatorType
		if hb.UseIndicator {
			indicator = resolveIndicatorType(status)
		}
		oc.skipHeartbeatRun(ctx, portal, state, hb, durationMs, hasMedia, sendOutcome, heartbeatSkipParams{
			status:    status,
			reason:    hb.Reason,
			restore:   true,
			indicator: indicator,
			to:        hb.TargetRoom.String(),
			silent:    silent,
			sent:      !silent,
		})
		return
	}

	// Deduplicate identical heartbeat content within 24h
	if hasContent && !shouldSkipMain && !hasMedia {
		storeRef := sessionStoreRef{AgentID: hb.StoreAgentID}
		if oc.isDuplicateHeartbeat(storeRef, hb.SessionKey, cleaned, state.startedAtMs) {
			var indicator *HeartbeatIndicatorType
			if hb.UseIndicator {
				indicator = resolveIndicatorType("skipped")
			}
			oc.skipHeartbeatRun(ctx, portal, state, hb, durationMs, hasMedia, sendOutcome, heartbeatSkipParams{
				status:    "skipped",
				reason:    "duplicate",
				restore:   true,
				indicator: indicator,
				preview:   cleaned,
				to:        "",
				silent:    true,
			})
			return
		}
	}

	if !deliverable {
		oc.skipHeartbeatRun(ctx, portal, state, hb, durationMs, hasMedia, sendOutcome, heartbeatSkipParams{
			status:  "skipped",
			reason:  targetReason,
			restore: false,
			preview: previewText(),
			to:      hb.TargetRoom.String(),
			silent:  true,
		})
		return
	}

	if !hb.ShowAlerts {
		var indicator *HeartbeatIndicatorType
		if hb.UseIndicator {
			indicator = resolveIndicatorType("sent")
		}
		oc.skipHeartbeatRun(ctx, portal, state, hb, durationMs, hasMedia, sendOutcome, heartbeatSkipParams{
			status:    "skipped",
			reason:    "alerts-disabled",
			restore:   true,
			indicator: indicator,
			preview:   previewText(),
			to:        hb.TargetRoom.String(),
			silent:    true,
		})
		return
	}

	if hasReasoning {
		oc.sendPlainAssistantMessage(ctx, portal, reasoningText)
	}

	if cleaned != "" {
		if !state.hasInitialMessageTarget() {
			oc.sendPlainAssistantMessage(ctx, portal, cleaned)
		} else {
			rendered := format.RenderMarkdown(cleaned, true, true)
			oc.sendFinalAssistantTurnContent(ctx, portal, state, meta, rendered, nil, "heartbeat")
		}
	}

	// Record heartbeat for dedupe
	if hb.SessionKey != "" && cleaned != "" && !shouldSkipMain {
		oc.recordHeartbeatText(sessionStoreRef{AgentID: hb.StoreAgentID}, hb.SessionKey, cleaned, state.startedAtMs)
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
	if state.networkMessageID != "" {
		if err := oc.redactViaPortal(ctx, portal, state.networkMessageID); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", state.initialEventID).Msg("Failed to redact streaming message via network ID")
		}
		return
	}
	if state.initialEventID == "" {
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
	sender := oc.senderForPortal(ctx, portal)
	msg := NewAITextMessage(portal, text, sender)
	oc.UserLogin.QueueRemoteEvent(msg)
	oc.recordAgentActivity(ctx, portal, portalMeta(portal))
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
		citations.AppendSourceURLPart(&parts, seen, url, title, providerMetadata)
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
		citations.AppendSourceDocumentPart(&parts, seen, doc)
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
			meta["siteName"] = site
			meta["site_name"] = site
		}
		if len(meta) == 0 {
			meta = nil
		}
		appendURL(url, title, meta)
	}

	return parts
}

func (oc *AIClient) buildFinalEditUIMessage(state *streamingState, meta *PortalMetadata, linkPreviews []*event.BeeperLinkPreview) map[string]any {
	return buildCompactFinalUIMessage(oc.buildStreamUIMessage(state, meta, linkPreviews))
}

func finalRenderedBodyFallback(state *streamingState) string {
	if state == nil {
		return "..."
	}
	if body := strings.TrimSpace(state.visibleAccumulated.String()); body != "" {
		return body
	}
	if body := strings.TrimSpace(state.accumulated.String()); body != "" {
		return body
	}
	if reasoning := strings.TrimSpace(state.reasoning.String()); reasoning != "" {
		return "..."
	}
	return "..."
}

func (oc *AIClient) persistTerminalAssistantTurn(ctx context.Context, log zerolog.Logger, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata) {
	if state == nil {
		return
	}
	if state.hasInitialMessageTarget() || state.heartbeat != nil {
		oc.sendFinalAssistantTurn(ctx, portal, state, meta)
	}
	if state.hasInitialMessageTarget() && !state.suppressSave {
		oc.saveAssistantMessage(ctx, log, portal, state, meta)
	}
}

// sendFinalAssistantTurnContent is a helper for simple mode that sends content without directive processing.
func (oc *AIClient) sendFinalAssistantTurnContent(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata, rendered event.MessageEventContent, replyToEventID *id.EventID, mode string) {
	// Safety-split oversized responses into multiple Matrix events
	var continuationBody string
	if len(rendered.Body) > streamtransport.MaxMatrixEventBodyBytes {
		firstBody, rest := streamtransport.SplitAtMarkdownBoundary(rendered.Body, streamtransport.MaxMatrixEventBodyBytes)
		continuationBody = rest
		rendered = format.RenderMarkdown(firstBody, true, true)
	}

	replyTo := id.EventID("")
	if replyToEventID != nil {
		replyTo = *replyToEventID
	}
	relatesTo := msgconv.RelatesToReplace(state.initialEventID, replyTo)
	if relatesTo == nil && state.networkMessageID != "" {
		oc.loggerForContext(ctx).Debug().
			Str("turn_id", state.turnID).
			Str("target_message_id", string(state.networkMessageID)).
			Msg("Final assistant edit using network target without initial event ID")
	}

	// Generate link previews for URLs in the response
	intent, _ := oc.getIntentForPortal(ctx, portal, bridgev2.RemoteEventMessage)
	linkPreviews := generateOutboundLinkPreviews(ctx, rendered.Body, intent, portal, state.sourceCitations, getLinkPreviewConfig(&oc.connector.Config))

	uiMessage := oc.buildFinalEditUIMessage(state, meta, linkPreviews)

	topLevelExtra := buildFinalEditTopLevelExtra(uiMessage, linkPreviews, relatesTo)
	sender := oc.senderForPortal(ctx, portal)
	editContent := &bridgev2.ConvertedEdit{
		ModifiedParts: []*bridgev2.ConvertedEditPart{{
			Part: nil,
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType:       event.MsgText,
				Body:          rendered.Body,
				Format:        rendered.Format,
				FormattedBody: rendered.FormattedBody,
			},
			Extra:         nil,
			TopLevelExtra: topLevelExtra,
		}},
	}
	editTarget := state.networkMessageID
	if editTarget == "" {
		editTarget = bridgeadapter.MatrixMessageID(state.initialEventID)
	}
	if editTarget == "" {
		oc.loggerForContext(ctx).Warn().
			Str("turn_id", state.turnID).
			Msg("Skipping final assistant edit: no network or initial event target")
	} else {
		oc.UserLogin.QueueRemoteEvent(&bridgeadapter.RemoteEdit{
			Portal:        portal.PortalKey,
			Sender:        sender,
			TargetMessage: editTarget,
			LogKey:        "ai_edit_target",
			PreBuilt:      editContent,
		})
	}
	oc.recordAgentActivity(ctx, portal, meta)
	oc.loggerForContext(ctx).Debug().
		Str("initial_event_id", state.initialEventID.String()).
		Str("turn_id", state.turnID).
		Str("mode", strings.TrimSpace(mode)).
		Int("link_previews", len(linkPreviews)).
		Msg("Queued final assistant turn edit")

	// Send continuation messages for overflow
	for continuationBody != "" {
		var chunk string
		chunk, continuationBody = streamtransport.SplitAtMarkdownBoundary(continuationBody, streamtransport.MaxMatrixEventBodyBytes)
		oc.sendContinuationMessage(ctx, portal, chunk)
	}
}

func buildFinalEditTopLevelExtra(uiMessage map[string]any, linkPreviews []*event.BeeperLinkPreview, relatesTo map[string]any) map[string]any {
	topLevelExtra := map[string]any{
		"com.beeper.dont_render_edited": true,
		"com.beeper.ai":                 uiMessage,
		"m.mentions":                    map[string]any{},
	}
	if len(linkPreviews) > 0 {
		topLevelExtra["com.beeper.linkpreviews"] = PreviewsToMapSlice(linkPreviews)
	}
	if relatesTo != nil {
		topLevelExtra["m.relates_to"] = relatesTo
	}
	return topLevelExtra
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

// getAgentResponseMode returns the response mode for the current room target.
// Defaults to ResponseModeNatural if no agent-specific mode is configured.
func (oc *AIClient) getAgentResponseMode(meta *PortalMetadata) agents.ResponseMode {
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
