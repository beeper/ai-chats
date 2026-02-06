package connector

import (
	"encoding/json"
	"mime"
	"net/url"
	"path/filepath"
	"strings"
)

type sourceCitation struct {
	URL   string
	Title string
}

type sourceDocument struct {
	ID        string
	Title     string
	Filename  string
	MediaType string
}

func extractURLCitation(annotation any) (sourceCitation, bool) {
	raw, ok := annotation.(map[string]any)
	if !ok {
		return sourceCitation{}, false
	}
	typ, _ := raw["type"].(string)
	if typ != "url_citation" {
		return sourceCitation{}, false
	}
	urlStr, ok := readStringArg(raw, "url")
	if !ok {
		return sourceCitation{}, false
	}
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return sourceCitation{}, false
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return sourceCitation{}, false
	}
	title, _ := readStringArg(raw, "title")
	return sourceCitation{URL: urlStr, Title: title}, true
}

func extractDocumentCitation(annotation any) (sourceDocument, bool) {
	raw, ok := annotation.(map[string]any)
	if !ok {
		return sourceDocument{}, false
	}
	typ, _ := raw["type"].(string)
	switch typ {
	case "file_citation", "container_file_citation", "file_path":
	default:
		return sourceDocument{}, false
	}

	fileID, _ := readStringArg(raw, "file_id")
	filename, _ := readStringArg(raw, "filename")
	title := filename
	if strings.TrimSpace(title) == "" {
		title = fileID
	}
	if strings.TrimSpace(title) == "" {
		return sourceDocument{}, false
	}
	mediaType := "application/octet-stream"
	if ext := strings.TrimSpace(filepath.Ext(filename)); ext != "" {
		if inferred := mime.TypeByExtension(ext); inferred != "" {
			mediaType = inferred
		}
	}

	return sourceDocument{
		ID:        fileID,
		Title:     title,
		Filename:  filename,
		MediaType: mediaType,
	}, true
}

func extractWebSearchCitationsFromToolOutput(toolName, output string) []sourceCitation {
	if normalizeToolAlias(strings.TrimSpace(toolName)) != ToolNameWebSearch {
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

	citations := make([]sourceCitation, 0, len(rawResults))
	for _, rawResult := range rawResults {
		entry, ok := rawResult.(map[string]any)
		if !ok {
			continue
		}
		urlStr, ok := readStringArg(entry, "url")
		if !ok {
			continue
		}
		parsed, err := url.Parse(urlStr)
		if err != nil {
			continue
		}
		switch parsed.Scheme {
		case "http", "https":
		default:
			continue
		}
		title, _ := readStringArg(entry, "title")
		citations = append(citations, sourceCitation{URL: urlStr, Title: title})
	}
	return citations
}

func mergeSourceCitations(existing, incoming []sourceCitation) []sourceCitation {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	merged := make([]sourceCitation, 0, len(existing)+len(incoming))
	for _, citation := range existing {
		urlStr := strings.TrimSpace(citation.URL)
		if urlStr == "" {
			continue
		}
		if _, ok := seen[urlStr]; ok {
			continue
		}
		seen[urlStr] = struct{}{}
		merged = append(merged, citation)
	}
	for _, citation := range incoming {
		urlStr := strings.TrimSpace(citation.URL)
		if urlStr == "" {
			continue
		}
		if _, ok := seen[urlStr]; ok {
			continue
		}
		seen[urlStr] = struct{}{}
		merged = append(merged, citation)
	}
	return merged
}
