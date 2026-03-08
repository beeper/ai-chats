package connector

import (
	"mime"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/citations"
	"github.com/beeper/ai-bridge/pkg/shared/maputil"
)

func extractURLCitation(annotation any) (citations.SourceCitation, bool) {
	raw, ok := annotation.(map[string]any)
	if !ok {
		return citations.SourceCitation{}, false
	}
	typ, _ := raw["type"].(string)
	if typ != "url_citation" {
		return citations.SourceCitation{}, false
	}
	urlStr := maputil.StringArg(raw, "url")
	if urlStr == "" {
		return citations.SourceCitation{}, false
	}
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return citations.SourceCitation{}, false
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return citations.SourceCitation{}, false
	}
	title := maputil.StringArg(raw, "title")
	return citations.SourceCitation{URL: urlStr, Title: title}, true
}

func extractDocumentCitation(annotation any) (citations.SourceDocument, bool) {
	raw, ok := annotation.(map[string]any)
	if !ok {
		return citations.SourceDocument{}, false
	}
	typ, _ := raw["type"].(string)
	switch typ {
	case "file_citation", "container_file_citation", "file_path":
	default:
		return citations.SourceDocument{}, false
	}

	fileID := maputil.StringArg(raw, "file_id")
	filename := maputil.StringArg(raw, "filename")
	title := filename
	if strings.TrimSpace(title) == "" {
		title = fileID
	}
	if strings.TrimSpace(title) == "" {
		return citations.SourceDocument{}, false
	}
	mediaType := "application/octet-stream"
	if ext := strings.TrimSpace(filepath.Ext(filename)); ext != "" {
		if inferred := mime.TypeByExtension(ext); inferred != "" {
			mediaType = inferred
		}
	}

	return citations.SourceDocument{
		ID:        fileID,
		Title:     title,
		Filename:  filename,
		MediaType: mediaType,
	}, true
}

func extractWebSearchCitationsFromToolOutput(toolName, output string) []citations.SourceCitation {
	if normalizeToolAlias(strings.TrimSpace(toolName)) != ToolNameWebSearch {
		return nil
	}
	return citations.ExtractWebSearchCitations(output)
}
