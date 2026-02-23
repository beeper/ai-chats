package codex

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/id"
)

const maxMatrixEventBodyBytes = 60000

// splitAtMarkdownBoundary splits text at a paragraph/line boundary near maxBytes.
func splitAtMarkdownBoundary(text string, maxBytes int) (string, string) {
	if len(text) <= maxBytes {
		return text, ""
	}
	cutoff := text[:maxBytes]
	if idx := strings.LastIndex(cutoff, "\n\n"); idx > maxBytes/2 {
		return text[:idx], text[idx:]
	}
	if idx := strings.LastIndex(cutoff, "\n"); idx > maxBytes/2 {
		return text[:idx], text[idx:]
	}
	return cutoff, text[maxBytes:]
}

type sourceCitation struct {
	URL         string
	Title       string
	Description string
	Published   string
	SiteName    string
	Author      string
	Image       string
	Favicon     string
}

type sourceDocument struct {
	ID        string
	Title     string
	Filename  string
	MediaType string
}

func citationProviderMetadata(c sourceCitation) map[string]any {
	meta := map[string]any{}
	if v := strings.TrimSpace(c.Description); v != "" {
		meta["description"] = v
	}
	if v := strings.TrimSpace(c.Published); v != "" {
		meta["published"] = v
	}
	if v := strings.TrimSpace(c.SiteName); v != "" {
		meta["site_name"] = v
	}
	if v := strings.TrimSpace(c.Author); v != "" {
		meta["author"] = v
	}
	if v := strings.TrimSpace(c.Image); v != "" {
		meta["image"] = v
	}
	if v := strings.TrimSpace(c.Favicon); v != "" {
		meta["favicon"] = v
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

func buildSourceParts(citations []sourceCitation, documents []sourceDocument) []map[string]any {
	if len(citations) == 0 && len(documents) == 0 {
		return nil
	}
	parts := make([]map[string]any, 0, len(citations)+len(documents))
	seen := make(map[string]struct{}, len(citations)+len(documents))
	for _, c := range citations {
		url := strings.TrimSpace(c.URL)
		if url == "" {
			continue
		}
		if _, ok := seen[url]; ok {
			continue
		}
		seen[url] = struct{}{}
		p := map[string]any{
			"type":     "source-url",
			"sourceId": fmt.Sprintf("source-%d", len(parts)+1),
			"url":      url,
		}
		if title := strings.TrimSpace(c.Title); title != "" {
			p["title"] = title
		}
		if meta := citationProviderMetadata(c); len(meta) > 0 {
			p["providerMetadata"] = meta
		}
		parts = append(parts, p)
	}
	for _, d := range documents {
		key := strings.TrimSpace(d.ID)
		if key == "" {
			key = strings.TrimSpace(d.Filename)
		}
		if key == "" {
			key = strings.TrimSpace(d.Title)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		p := map[string]any{
			"type":      "source-document",
			"sourceId":  fmt.Sprintf("source-%d", len(parts)+1),
			"mediaType": d.MediaType,
			"title":     d.Title,
		}
		if fn := strings.TrimSpace(d.Filename); fn != "" {
			p["filename"] = fn
		}
		parts = append(parts, p)
	}
	return parts
}

type generatedFilePart struct {
	url       string
	mediaType string
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
		parts = append(parts, map[string]any{
			"type":      "file",
			"url":       file.url,
			"mediaType": strings.TrimSpace(file.mediaType),
		})
	}
	return parts
}

type streamingState struct {
	turnID             string
	agentID            string
	startedAtMs        int64
	firstTokenAtMs     int64
	completedAtMs      int64
	promptTokens       int64
	completionTokens   int64
	reasoningTokens    int64
	totalTokens        int64
	accumulated        strings.Builder
	visibleAccumulated strings.Builder
	reasoning          strings.Builder
	toolCalls          []ToolCallMetadata
	sourceCitations    []sourceCitation
	sourceDocuments    []sourceDocument
	generatedFiles     []generatedFilePart
	initialEventID     id.EventID
	sequenceNum        int
	firstToken         bool
	suppressSend       bool

	uiFinished              bool
	uiTextID                string
	uiReasoningID           string
	uiToolStarted           map[string]bool
	uiSourceURLSeen         map[string]bool
	uiToolCallIDByApproval  map[string]string
	uiToolApprovalRequested map[string]bool
	uiToolNameByToolCallID  map[string]string
	uiToolTypeByToolCallID  map[string]ToolType
	uiToolOutputFinalized   map[string]bool

	codexToolOutputBuffers    map[string]*strings.Builder
	codexLatestDiff           string
	codexReasoningSummarySeen bool
	codexTimelineNotices      map[string]bool
}

func newStreamingState(ctx context.Context, meta *PortalMetadata, sourceEventID id.EventID, senderID string, roomID id.RoomID) *streamingState {
	_ = ctx
	_ = meta
	_ = senderID
	_ = roomID
	return &streamingState{
		turnID:                  NewTurnID(),
		startedAtMs:             nowMillis(),
		firstToken:              true,
		initialEventID:          sourceEventID,
		uiToolStarted:           make(map[string]bool),
		uiSourceURLSeen:         make(map[string]bool),
		uiToolCallIDByApproval:  make(map[string]string),
		uiToolApprovalRequested: make(map[string]bool),
		uiToolNameByToolCallID:  make(map[string]string),
		uiToolTypeByToolCallID:  make(map[string]ToolType),
		uiToolOutputFinalized:   make(map[string]bool),
		codexTimelineNotices:    make(map[string]bool),
		codexToolOutputBuffers:  make(map[string]*strings.Builder),
	}
}
