package connector

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
)

func (oc *AIClient) emitUISourceURL(ctx context.Context, portal *bridgev2.Portal, state *streamingState, citation sourceCitation) {
	if state == nil {
		return
	}
	url := strings.TrimSpace(citation.URL)
	if url == "" {
		return
	}
	if state.uiSourceURLSeen[url] {
		return
	}
	state.uiSourceURLSeen[url] = true
	part := map[string]any{
		"type":     "source-url",
		"sourceId": fmt.Sprintf("source-url-%d", len(state.uiSourceURLSeen)),
		"url":      url,
	}
	if title := strings.TrimSpace(citation.Title); title != "" {
		part["title"] = title
	}
	if providerMeta := citationProviderMetadata(citation); len(providerMeta) > 0 {
		part["providerMetadata"] = providerMeta
	}
	oc.emitStreamEvent(ctx, portal, state, part)
}

func (oc *AIClient) emitUISourceDocument(ctx context.Context, portal *bridgev2.Portal, state *streamingState, doc sourceDocument) {
	if state == nil {
		return
	}
	key := strings.TrimSpace(doc.ID)
	if key == "" {
		key = strings.TrimSpace(doc.Filename)
	}
	if key == "" {
		key = strings.TrimSpace(doc.Title)
	}
	if key == "" {
		return
	}
	if state.uiSourceDocumentSeen[key] {
		return
	}
	state.uiSourceDocumentSeen[key] = true
	part := map[string]any{
		"type":      "source-document",
		"sourceId":  fmt.Sprintf("source-doc-%d", len(state.uiSourceDocumentSeen)),
		"mediaType": strings.TrimSpace(doc.MediaType),
		"title":     strings.TrimSpace(doc.Title),
	}
	if part["mediaType"] == "" {
		part["mediaType"] = "application/octet-stream"
	}
	if title, _ := part["title"].(string); title == "" {
		part["title"] = key
	}
	if filename := strings.TrimSpace(doc.Filename); filename != "" {
		part["filename"] = filename
	}
	oc.emitStreamEvent(ctx, portal, state, part)
}

func (oc *AIClient) emitUIFile(ctx context.Context, portal *bridgev2.Portal, state *streamingState, fileURL, mediaType string) {
	if state == nil {
		return
	}
	fileURL = strings.TrimSpace(fileURL)
	if fileURL == "" {
		return
	}
	if state.uiFileSeen[fileURL] {
		return
	}
	state.uiFileSeen[fileURL] = true
	if strings.TrimSpace(mediaType) == "" {
		mediaType = "application/octet-stream"
	}
	oc.emitStreamEvent(ctx, portal, state, map[string]any{
		"type":      "file",
		"url":       fileURL,
		"mediaType": mediaType,
	})
}

func collectToolOutputCitations(state *streamingState, toolName, output string) {
	if state == nil {
		return
	}
	citations := extractWebSearchCitationsFromToolOutput(toolName, output)
	if len(citations) == 0 {
		return
	}
	state.sourceCitations = mergeSourceCitations(state.sourceCitations, citations)
}
