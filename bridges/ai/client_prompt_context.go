package ai

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/sdk"
)

func (oc *AIClient) applyAbortHint(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, body string) string {
	if meta == nil || !meta.AbortedLastRun {
		return body
	}
	meta.AbortedLastRun = false
	if portal != nil {
		oc.savePortalQuiet(ctx, portal, "abort hint")
	}
	note := "Note: The previous assistant turn was aborted by the user. Resume carefully or ask for clarification."
	if strings.TrimSpace(body) == "" {
		return note
	}
	return note + "\n\n" + body
}

type inboundPromptResult struct {
	PromptContext   PromptContext
	ResolvedBody    string // user message after body override + abort hint
	UntrustedPrefix string // context prefix to prepend to the resolved user body
}

// prepareInboundPromptContext builds the base context, resolves inbound context,
// appends trusted inbound metadata to the system prompt, resolves body overrides,
// and applies the abort hint. Untrusted inbound prefixes are returned separately
// so callers can place them deterministically in the user prompt body.
func (oc *AIClient) prepareInboundPromptContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	userText string,
	eventID id.EventID,
) (inboundPromptResult, error) {
	promptContext := PromptContext{
		SystemPrompt: oc.buildConversationSystemPromptText(ctx, portal, meta, true),
	}
	historyMessages, err := oc.replayHistoryMessages(ctx, portal, meta, historyReplayOptions{
		mode:             historyReplayNormal,
		excludeMessageID: sdk.MatrixMessageID(eventID),
	})
	if err != nil {
		return inboundPromptResult{}, err
	}
	promptContext.Messages = append(promptContext.Messages, historyMessages...)
	inboundCtx := oc.resolvePromptInboundContext(ctx, portal, userText, eventID)
	AppendPromptText(&promptContext.SystemPrompt, airuntime.BuildInboundMetaSystemPrompt(inboundCtx))

	resolved := strings.TrimSpace(userText)
	if body := strings.TrimSpace(inboundCtx.BodyForAgent); body != "" {
		resolved = body
	}

	resolved = oc.applyAbortHint(ctx, portal, meta, resolved)
	untrustedPrefix := strings.TrimSpace(airuntime.BuildInboundUserContextPrefix(inboundCtx))

	return inboundPromptResult{
		PromptContext:   promptContext,
		ResolvedBody:    resolved,
		UntrustedPrefix: untrustedPrefix,
	}, nil
}

// buildLinkContext extracts URLs from the message, fetches previews, and returns formatted context.
func (oc *AIClient) buildLinkContext(ctx context.Context, message string, rawEventContent map[string]any) string {
	config := getLinkPreviewConfig(&oc.connector.Config)
	if !config.Enabled {
		return ""
	}

	// Extract URLs from message
	urls := ExtractURLs(message, config.MaxURLsInbound)
	if len(urls) == 0 {
		return ""
	}

	// Check for existing previews in the event
	var existingPreviews []*event.BeeperLinkPreview
	if rawEventContent != nil {
		existingPreviews = ParseExistingLinkPreviews(rawEventContent)
	}

	// Build map of existing previews by URL
	existingByURL := make(map[string]*event.BeeperLinkPreview)
	for _, p := range existingPreviews {
		if p.MatchedURL != "" {
			existingByURL[p.MatchedURL] = p
		}
		if p.CanonicalURL != "" {
			existingByURL[p.CanonicalURL] = p
		}
	}

	// Find URLs that need fetching
	var urlsToFetch []string
	var allPreviews []*event.BeeperLinkPreview
	for _, u := range urls {
		if existing, ok := existingByURL[u]; ok {
			allPreviews = append(allPreviews, existing)
		} else {
			urlsToFetch = append(urlsToFetch, u)
		}
	}

	// Fetch missing previews
	if len(urlsToFetch) > 0 {
		previewer := NewLinkPreviewer(config)
		fetchCtx, cancel := context.WithTimeout(ctx, config.FetchTimeout*time.Duration(len(urlsToFetch)))
		defer cancel()

		// For inbound context, we don't need to upload images - just extract the text data
		fetchedWithImages := previewer.FetchPreviews(fetchCtx, urlsToFetch)
		fetched := ExtractBeeperPreviews(fetchedWithImages)
		allPreviews = append(allPreviews, fetched...)
	}

	if len(allPreviews) == 0 {
		return ""
	}

	return FormatPreviewsForContext(allPreviews, config.MaxContentChars)
}

// buildPromptUpToMessage builds a prompt including messages up to and including the specified message
func (oc *AIClient) buildContextUpToMessage(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	targetMessageID networkid.MessageID,
	newBody string,
) (PromptContext, error) {
	base := PromptContext{
		SystemPrompt: oc.buildConversationSystemPromptText(ctx, portal, meta, false),
	}
	historyMessages, err := oc.replayHistoryMessages(ctx, portal, meta, historyReplayOptions{
		mode:            historyReplayRewrite,
		targetMessageID: targetMessageID,
	})
	if err != nil {
		return PromptContext{}, err
	}
	base.Messages = append(base.Messages, historyMessages...)
	body := strings.TrimSpace(newBody)
	body = airuntime.SanitizeChatMessageForDisplay(body, true)
	if userMessage, turnData, ok := buildUserPromptTurn([]PromptBlock{{
		Type: PromptBlockText,
		Text: body,
	}}); ok {
		base.Messages = append(base.Messages, userMessage)
		base.CurrentTurnData = turnData
	}
	return base, nil
}

// downloadAndEncodeMedia downloads media and returns base64-encoded data.
// maxSizeMB limits the download size (0 = no limit).
func (oc *AIClient) downloadAndEncodeMedia(ctx context.Context, mxcURL string, encryptedFile *event.EncryptedFileInfo, maxSizeMB int) (string, string, error) {
	maxBytes := 0
	if maxSizeMB > 0 {
		maxBytes = maxSizeMB * 1024 * 1024
	}
	data, mimeType, err := oc.downloadMediaBytes(ctx, mxcURL, encryptedFile, maxBytes, "")
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(data), mimeType, nil
}
