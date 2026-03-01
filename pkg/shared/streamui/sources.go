package streamui

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/shared/citations"
)

// EmitUISourceURL sends a "source-url" event (deduplicated by URL).
func (e *Emitter) EmitUISourceURL(ctx context.Context, portal *bridgev2.Portal, citation citations.SourceCitation) {
	if e.State == nil {
		return
	}
	url := strings.TrimSpace(citation.URL)
	if url == "" {
		return
	}
	if e.State.UISourceURLSeen[url] {
		return
	}
	e.State.UISourceURLSeen[url] = true
	part := map[string]any{
		"type":     "source-url",
		"sourceId": fmt.Sprintf("source-url-%d", len(e.State.UISourceURLSeen)),
		"url":      url,
	}
	if title := strings.TrimSpace(citation.Title); title != "" {
		part["title"] = title
	}
	if providerMeta := citations.ProviderMetadata(citation); len(providerMeta) > 0 {
		part["providerMetadata"] = providerMeta
	}
	e.Emit(ctx, portal, part)
}

// EmitUISourceDocument sends a "source-document" event (deduplicated by id/filename/title).
func (e *Emitter) EmitUISourceDocument(ctx context.Context, portal *bridgev2.Portal, doc citations.SourceDocument) {
	if e.State == nil {
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
	if e.State.UISourceDocumentSeen[key] {
		return
	}
	e.State.UISourceDocumentSeen[key] = true
	part := map[string]any{
		"type":      "source-document",
		"sourceId":  fmt.Sprintf("source-doc-%d", len(e.State.UISourceDocumentSeen)),
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
	e.Emit(ctx, portal, part)
}

// EmitUIFile sends a "file" event (deduplicated by URL).
func (e *Emitter) EmitUIFile(ctx context.Context, portal *bridgev2.Portal, fileURL, mediaType string) {
	if e.State == nil {
		return
	}
	fileURL = strings.TrimSpace(fileURL)
	if fileURL == "" {
		return
	}
	if e.State.UIFileSeen[fileURL] {
		return
	}
	e.State.UIFileSeen[fileURL] = true
	if strings.TrimSpace(mediaType) == "" {
		mediaType = "application/octet-stream"
	}
	e.Emit(ctx, portal, map[string]any{
		"type":      "file",
		"url":       fileURL,
		"mediaType": mediaType,
	})
}
