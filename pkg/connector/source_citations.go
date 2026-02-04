package connector

import (
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
