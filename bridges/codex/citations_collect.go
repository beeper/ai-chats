package codex

import (
	"encoding/json"
	neturl "net/url"
	"path/filepath"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/citations"
)

func collectToolOutputCitations(state *streamingState, toolName, output string) {
	if state == nil {
		return
	}
	extracted := extractWebSearchCitationsFromToolOutput(toolName, output)
	if len(extracted) == 0 {
		return
	}
	state.sourceCitations = citations.MergeSourceCitations(state.sourceCitations, extracted)
}

func collectToolOutputArtifacts(state *streamingState, output any) {
	if state == nil || output == nil {
		return
	}
	walkToolOutputArtifacts(output, func(doc citations.SourceDocument, file citations.GeneratedFilePart) {
		if doc != (citations.SourceDocument{}) {
			if !hasSourceDocument(state.sourceDocuments, doc) {
				state.sourceDocuments = append(state.sourceDocuments, doc)
			}
		}
		if file != (citations.GeneratedFilePart{}) {
			if !hasGeneratedFile(state.generatedFiles, file) {
				state.generatedFiles = append(state.generatedFiles, file)
			}
		}
	})
}

func walkToolOutputArtifacts(value any, record func(citations.SourceDocument, citations.GeneratedFilePart)) {
	switch typed := value.(type) {
	case map[string]any:
		if doc, file := extractArtifactRecord(typed); doc != (citations.SourceDocument{}) || file != (citations.GeneratedFilePart{}) {
			record(doc, file)
		}
		for _, nested := range typed {
			walkToolOutputArtifacts(nested, record)
		}
	case []any:
		for _, nested := range typed {
			walkToolOutputArtifacts(nested, record)
		}
	}
}

func extractArtifactRecord(raw map[string]any) (citations.SourceDocument, citations.GeneratedFilePart) {
	url := firstString(raw, "url", "uri", "downloadUrl", "download_url", "fileUrl", "file_url")
	filename := firstString(raw, "filename", "fileName", "name")
	title := firstString(raw, "title", "label")
	mediaType := firstString(raw, "mediaType", "media_type", "mimeType", "mime_type", "contentType", "content_type")
	id := firstString(raw, "id", "fileId", "file_id", "documentId", "document_id")

	if title == "" {
		title = filename
	}
	if filename == "" && title != "" && strings.Contains(title, ".") {
		filename = title
	}
	if mediaType == "" && filename != "" {
		mediaType = mediaTypeFromFilename(filename)
	}

	var doc citations.SourceDocument
	if id != "" || filename != "" || title != "" {
		doc = citations.SourceDocument{
			ID:        id,
			Title:     title,
			Filename:  filename,
			MediaType: mediaType,
		}
	}

	var file citations.GeneratedFilePart
	if parsedURL, err := neturl.Parse(strings.TrimSpace(url)); err == nil && (parsedURL.Scheme == "http" || parsedURL.Scheme == "https") {
		if mediaType == "" {
			mediaType = mediaTypeFromFilename(filenameFromURL(parsedURL.Path))
		}
		file = citations.GeneratedFilePart{
			URL:       strings.TrimSpace(url),
			MediaType: mediaType,
		}
		if doc == (citations.SourceDocument{}) && (filename != "" || title != "") {
			doc = citations.SourceDocument{
				ID:        id,
				Title:     title,
				Filename:  filename,
				MediaType: mediaType,
			}
		}
	}

	return doc, file
}

func firstString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := raw[key].(string)
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func mediaTypeFromFilename(filename string) string {
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(filename)))
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".txt", ".md":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

func filenameFromURL(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	if base == "." || base == "/" {
		return ""
	}
	return base
}

func hasSourceDocument(existing []citations.SourceDocument, doc citations.SourceDocument) bool {
	key := strings.TrimSpace(doc.ID)
	if key == "" {
		key = strings.TrimSpace(doc.Filename)
	}
	if key == "" {
		key = strings.TrimSpace(doc.Title)
	}
	if key == "" {
		return true
	}
	for _, item := range existing {
		itemKey := strings.TrimSpace(item.ID)
		if itemKey == "" {
			itemKey = strings.TrimSpace(item.Filename)
		}
		if itemKey == "" {
			itemKey = strings.TrimSpace(item.Title)
		}
		if itemKey == key {
			return true
		}
	}
	return false
}

func hasGeneratedFile(existing []citations.GeneratedFilePart, file citations.GeneratedFilePart) bool {
	url := strings.TrimSpace(file.URL)
	if url == "" {
		return true
	}
	for _, item := range existing {
		if strings.TrimSpace(item.URL) == url {
			return true
		}
	}
	return false
}

func extractWebSearchCitationsFromToolOutput(toolName, output string) []citations.SourceCitation {
	if normalizeToolAlias(strings.TrimSpace(toolName)) != "websearch" {
		return nil
	}
	output = strings.TrimSpace(output)
	if output == "" || !strings.HasPrefix(output, "{") {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil
	}
	rawResults, ok := payload["results"].([]any)
	if !ok || len(rawResults) == 0 {
		return nil
	}
	result := make([]citations.SourceCitation, 0, len(rawResults))
	for _, item := range rawResults {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		url, _ := m["url"].(string)
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}
		parsedURL, err := neturl.Parse(url)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			continue
		}
		title, _ := m["title"].(string)
		description, _ := m["description"].(string)
		siteName, _ := m["siteName"].(string)
		result = append(result, citations.SourceCitation{
			URL:         url,
			Title:       strings.TrimSpace(title),
			Description: strings.TrimSpace(description),
			SiteName:    strings.TrimSpace(siteName),
		})
	}
	return result
}
