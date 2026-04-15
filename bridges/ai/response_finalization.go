package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/sdk"
	"github.com/beeper/agentremote/turns"
)

// sendContinuationMessage sends overflow text as a new (non-edit) message from the bot.
func (oc *AIClient) sendContinuationMessage(ctx context.Context, portal *bridgev2.Portal, body string, replyTarget ReplyTarget, timing sdk.EventTiming) {
	if portal == nil || portal.MXID == "" {
		return
	}
	sender := oc.senderForPortal(ctx, portal)
	intent, ok := portal.GetIntentFor(ctx, sender, oc.UserLogin, bridgev2.RemoteEventMessage)
	if !ok || intent == nil {
		oc.loggerForContext(ctx).Warn().Int("body_len", len(body)).Msg("Failed to resolve continuation intent")
		return
	}
	if err := intent.EnsureJoined(ctx, portal.MXID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Int("body_len", len(body)).Msg("Failed to prepare continuation sender")
		return
	}
	rendered := format.RenderMarkdown(body, true, true)
	msgID := sdk.NewMessageID("ai")
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:   networkid.PartID("0"),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType:       event.MsgText,
				Body:          rendered.Body,
				Format:        rendered.Format,
				FormattedBody: rendered.FormattedBody,
				Mentions:      &event.Mentions{},
			},
			Extra: map[string]any{"com.beeper.continuation": true},
		}},
	}
	var relatesTo *event.RelatesTo
	if replyTarget.ThreadRoot != "" {
		relatesTo = (&event.RelatesTo{}).SetThread(replyTarget.ThreadRoot, replyTarget.EffectiveReplyTo())
	} else if replyTarget.ReplyTo != "" {
		relatesTo = (&event.RelatesTo{}).SetReplyTo(replyTarget.ReplyTo)
	}
	if relatesTo != nil && len(converted.Parts) > 0 && converted.Parts[0] != nil && converted.Parts[0].Content != nil {
		converted.Parts[0].Content.RelatesTo = relatesTo
	}
	if _, _, err := sdk.SendViaPortal(sdk.SendViaPortalParams{
		Login:       oc.UserLogin,
		Portal:      portal,
		Sender:      sender,
		IDPrefix:    oc.ClientBase.MessageIDPrefix,
		LogKey:      "ai_msg_id",
		MsgID:       msgID,
		Timestamp:   timing.Timestamp,
		StreamOrder: timing.StreamOrder,
		Converted:   converted,
	}); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Int("body_len", len(body)).Msg("Failed to queue continuation message")
		return
	}
	oc.loggerForContext(ctx).Debug().Int("body_len", len(body)).Msg("Queued continuation message for oversized response")
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
			Str("event_id", state.turn.InitialEventID().String()).
			Int("accumulated_len", state.accumulated.Len()).
			Msg("Flushing partial streaming message on cancellation")
		oc.saveAssistantMessage(ctx, log, portal, state, meta)
	}
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

	emitOutcome := func(out HeartbeatRunOutcome) {
		if state.heartbeatResultCh == nil {
			return
		}
		select {
		case state.heartbeatResultCh <- out:
		default:
		}
	}
	skipStatus := ""
	skipReason := ""
	skipPreview := cleaned
	if skipPreview == "" && hasReasoning {
		skipPreview = reasoningText
	}
	skipRestore := false
	skipSilent := true
	skipSent := false
	skipTo := hb.TargetRoom.String()
	skipIndicatorStatus := ""
	skipRun := false
	if shouldSkipMain && !hasContent && !hasReasoning {
		if hb.ShowOk && deliverable {
			_ = oc.sendPlainAssistantMessage(ctx, portal, agents.HeartbeatToken)
			skipSilent = false
			skipSent = true
		}
		skipStatus = "ok-token"
		if strings.TrimSpace(rawContent) == "" {
			skipStatus = "ok-empty"
		}
		skipReason = hb.Reason
		skipRestore = true
		skipIndicatorStatus = skipStatus
		skipRun = true
	} else if hasContent && !shouldSkipMain && !hasMedia &&
		oc.isDuplicateHeartbeat(hb.AgentID, hb.SessionKey, cleaned, state.startedAtMs) {
		skipStatus = "skipped"
		skipReason = "duplicate"
		skipPreview = cleaned
		skipRestore = true
		skipIndicatorStatus = "skipped"
		skipTo = ""
		skipRun = true
	} else if !deliverable {
		skipStatus = "skipped"
		skipReason = targetReason
		skipRun = true
	} else if !hb.ShowAlerts {
		skipStatus = "skipped"
		skipReason = "alerts-disabled"
		skipRestore = true
		skipIndicatorStatus = "sent"
		skipRun = true
	}
	if skipRun {
		if skipRestore {
			oc.restoreHeartbeatUpdatedAt(hb.StoreAgentID, hb.SessionKey, hb.PrevUpdatedAt)
		}
		oc.redactInitialStreamingMessage(ctx, portal, state)
		state.pendingImages = nil
		if len(skipPreview) > 200 {
			skipPreview = skipPreview[:200]
		}
		indicator := (*HeartbeatIndicatorType)(nil)
		if hb.UseIndicator && skipIndicatorStatus != "" {
			indicator = resolveIndicatorType(skipIndicatorStatus)
		}
		oc.emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:            time.Now().UnixMilli(),
			Status:        skipStatus,
			To:            skipTo,
			Reason:        skipReason,
			Preview:       skipPreview,
			Channel:       hb.Channel,
			Silent:        skipSilent,
			HasMedia:      hasMedia,
			DurationMs:    durationMs,
			IndicatorType: indicator,
		})
		emitOutcome(HeartbeatRunOutcome{
			Status:  "ran",
			Reason:  skipReason,
			Preview: skipPreview,
			Sent:    skipSent,
			Silent:  skipSilent,
			Skipped: true,
		})
		return
	}

	if hasReasoning {
		_ = oc.sendPlainAssistantMessage(ctx, portal, reasoningText)
	}

	if cleaned != "" {
		if !state.hasInitialMessageTarget() {
			_ = oc.sendPlainAssistantMessage(ctx, portal, cleaned)
		} else {
			rendered := format.RenderMarkdown(cleaned, true, true)
			oc.sendFinalAssistantTurnContent(ctx, portal, state, meta, cleaned, rendered, ReplyTarget{}, "heartbeat")
		}
	}

	// Record heartbeat for dedupe
	if hb.SessionKey != "" && cleaned != "" && !shouldSkipMain {
		oc.recordHeartbeatText(hb.AgentID, hb.SessionKey, cleaned, state.startedAtMs)
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
	emitOutcome(HeartbeatRunOutcome{Status: "ran", Text: cleaned, Sent: true})
}

func (oc *AIClient) redactInitialStreamingMessage(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if portal == nil || state == nil {
		return
	}
	if portal.MXID == "" || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return
	}
	sender := oc.senderForPortal(ctx, portal)
	intent, ok := portal.GetIntentFor(ctx, sender, oc.UserLogin, bridgev2.RemoteEventMessage)
	if !ok || intent == nil {
		oc.loggerForContext(ctx).Warn().Stringer("event_id", state.turn.InitialEventID()).Msg("Failed to resolve redaction intent for streaming message")
		return
	}
	if err := intent.EnsureJoined(ctx, portal.MXID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", state.turn.InitialEventID()).Msg("Failed to prepare redaction sender for streaming message")
		return
	}
	targetMessageID := state.turn.NetworkMessageID()
	if targetMessageID == "" {
		if state.turn.InitialEventID() == "" {
			return
		}
		part, err := oc.loadPortalMessagePartByMXID(ctx, portal, state.turn.InitialEventID())
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", state.turn.InitialEventID()).Msg("Failed to look up streaming message for redaction")
			return
		}
		if part == nil {
			oc.loggerForContext(ctx).Warn().Stringer("event_id", state.turn.InitialEventID()).Msg("Streaming message not found for redaction")
			return
		}
		targetMessageID = part.ID
	}
	result := oc.UserLogin.QueueRemoteEvent(&simplevent.MessageRemove{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventMessageRemove,
			PortalKey: portal.PortalKey,
			Sender:    sender,
		},
		TargetMessage: targetMessageID,
	})
	if !result.Success {
		err := fmt.Errorf("redact failed")
		if result.Error != nil {
			err = fmt.Errorf("redact failed: %w", result.Error)
		}
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", state.turn.InitialEventID()).Msg("Failed to redact streaming message")
	}
}

func (oc *AIClient) sendPlainAssistantMessage(ctx context.Context, portal *bridgev2.Portal, text string) error {
	if portal == nil || portal.MXID == "" {
		return nil
	}

	rendered := format.RenderMarkdown(text, true, true)
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:   networkid.PartID("0"),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType:       event.MsgText,
				Body:          rendered.Body,
				Format:        rendered.Format,
				FormattedBody: rendered.FormattedBody,
				Mentions:      &event.Mentions{},
			},
		}},
	}

	sender := oc.senderForPortal(ctx, portal)
	if _, _, err := sdk.SendViaPortal(sdk.SendViaPortalParams{
		Login:       oc.UserLogin,
		Portal:      portal,
		Sender:      sender,
		IDPrefix:    oc.ClientBase.MessageIDPrefix,
		LogKey:      oc.ClientBase.MessageLogKey,
		Timestamp:   time.Now(),
		StreamOrder: 0,
		Converted:   converted,
	}); err != nil {
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

func finalRenderedBodyFallback(state *streamingState) string {
	if state == nil {
		return "..."
	}
	if body := strings.TrimSpace(displayStreamingText(state)); body != "" {
		return body
	}
	return "..."
}

// sendFinalAssistantTurnContent sends the final assistant content after directive processing.
func (oc *AIClient) sendFinalAssistantTurnContent(ctx context.Context, portal *bridgev2.Portal, state *streamingState, meta *PortalMetadata, markdown string, rendered event.MessageEventContent, replyTarget ReplyTarget, mode string) {
	// Safety-split oversized responses into multiple Matrix events
	var continuationBody string
	if len(rendered.Body) > turns.MaxMatrixEventBodyBytes {
		firstBody, rest := turns.SplitAtMarkdownBoundary(markdown, turns.MaxMatrixEventBodyBytes)
		continuationBody = rest
		rendered = format.RenderMarkdown(firstBody, true, true)
	}

	// Generate link previews for URLs in the response
	sender := oc.senderForPortal(ctx, portal)
	intent, _ := portal.GetIntentFor(ctx, sender, oc.UserLogin, bridgev2.RemoteEventMessage)
	var sourceCitations []citations.SourceCitation
	if state != nil {
		sourceCitations = state.sourceCitations
	}
	linkPreviews := generateOutboundLinkPreviews(ctx, rendered.Body, intent, portal, sourceCitations, getLinkPreviewConfig(&oc.connector.Config))

	uiMessage := sdk.BuildCompactFinalUIMessage(oc.buildStreamUIMessage(state, meta, linkPreviews))

	if state != nil && state.turn != nil {
		var finishReason string
		if state != nil {
			finishReason = state.finishReason
		}
		state.turn.SetFinalEditPayload(sdk.BuildFinalEditPayload(
			rendered,
			uiMessage,
			PreviewsToMapSlice(linkPreviews),
			finishReason,
		))
	}
	oc.recordAgentActivity(ctx, portal, meta)
	if state != nil && state.turn != nil {
		oc.loggerForContext(ctx).Debug().
			Str("initial_event_id", state.turn.InitialEventID().String()).
			Str("turn_id", state.turn.ID()).
			Str("mode", strings.TrimSpace(mode)).
			Int("link_previews", len(linkPreviews)).
			Msg("Queued final assistant turn edit")
	}

	// Send continuation messages for overflow
	for continuationBody != "" {
		var chunk string
		chunk, continuationBody = turns.SplitAtMarkdownBoundary(continuationBody, turns.MaxMatrixEventBodyBytes)
		oc.sendContinuationMessage(ctx, portal, chunk, state.replyTarget, state.nextMessageTiming())
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
