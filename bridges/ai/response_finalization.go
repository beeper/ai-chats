package ai

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"

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

func (oc *AIClient) redactInitialStreamingMessage(ctx context.Context, portal *bridgev2.Portal, state *streamingState) {
	if portal == nil || state == nil {
		return
	}
	if portal.MXID == "" || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
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
	if err := oc.redactNetworkMessageViaPortal(ctx, portal, targetMessageID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", state.turn.InitialEventID()).Msg("Failed to redact streaming message")
	}
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
