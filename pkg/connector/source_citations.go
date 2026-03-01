package connector

import (
	"encoding/json"
	"mime"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/maputil"
)

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

func extractURLCitation(annotation any) (sourceCitation, bool) {
	raw, ok := annotation.(map[string]any)
	if !ok {
		return sourceCitation{}, false
	}
	typ, _ := raw["type"].(string)
	if typ != "url_citation" {
		return sourceCitation{}, false
	}
	urlStr, ok := maputil.StringArgMulti(raw, "url")
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
	title, _ := maputil.StringArgMulti(raw, "title")
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

	fileID, _ := maputil.StringArgMulti(raw, "file_id")
	filename, _ := maputil.StringArgMulti(raw, "filename")
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
		urlStr, ok := maputil.StringArgMulti(entry, "url")
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
		title, _ := maputil.StringArgMulti(entry, "title")
		description, _ := maputil.StringArgMulti(entry, "description")
		published, _ := maputil.StringArgMulti(entry, "published")
		siteName, _ := maputil.StringArgMulti(entry, "siteName")
		author, _ := maputil.StringArgMulti(entry, "author")
		image, _ := maputil.StringArgMulti(entry, "image")
		favicon, _ := maputil.StringArgMulti(entry, "favicon")
		citations = append(citations, sourceCitation{
			URL:         urlStr,
			Title:       title,
			Description: description,
			Published:   published,
			SiteName:    siteName,
			Author:      author,
			Image:       image,
			Favicon:     favicon,
		})
	}
	return citations
}

func mergeSourceCitations(existing, incoming []sourceCitation) []sourceCitation {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]int, len(existing)+len(incoming))
	merged := make([]sourceCitation, 0, len(existing)+len(incoming))
	for _, citation := range existing {
		urlStr := strings.TrimSpace(citation.URL)
		if urlStr == "" {
			continue
		}
		if idx, ok := seen[urlStr]; ok {
			merged[idx] = mergeCitationFields(merged[idx], citation)
			continue
		}
		seen[urlStr] = len(merged)
		merged = append(merged, citation)
	}
	for _, citation := range incoming {
		urlStr := strings.TrimSpace(citation.URL)
		if urlStr == "" {
			continue
		}
		if idx, ok := seen[urlStr]; ok {
			merged[idx] = mergeCitationFields(merged[idx], citation)
			continue
		}
		seen[urlStr] = len(merged)
		merged = append(merged, citation)
	}
	return merged
}

// citationProviderMetadata builds the providerMetadata map for a source-url part
// from a sourceCitation. The keys are chosen to match what the desktop client reads
// (e.g. "siteName" in camelCase for compatibility with pickString(md?.siteName)).
func citationProviderMetadata(c sourceCitation) map[string]any {
	meta := map[string]any{}
	if desc := strings.TrimSpace(c.Description); desc != "" {
		meta["description"] = desc
	}
	if published := strings.TrimSpace(c.Published); published != "" {
		meta["published"] = published
	}
	if site := strings.TrimSpace(c.SiteName); site != "" {
		meta["site_name"] = site
	}
	if author := strings.TrimSpace(c.Author); author != "" {
		meta["author"] = author
	}
	if image := strings.TrimSpace(c.Image); image != "" {
		meta["image"] = image
	}
	if favicon := strings.TrimSpace(c.Favicon); favicon != "" {
		meta["favicon"] = favicon
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

func mergeCitationFields(dst, src sourceCitation) sourceCitation {
	if strings.TrimSpace(dst.Title) == "" {
		dst.Title = src.Title
	}
	if strings.TrimSpace(dst.Description) == "" {
		dst.Description = src.Description
	}
	if strings.TrimSpace(dst.Published) == "" {
		dst.Published = src.Published
	}
	if strings.TrimSpace(dst.SiteName) == "" {
		dst.SiteName = src.SiteName
	}
	if strings.TrimSpace(dst.Author) == "" {
		dst.Author = src.Author
	}
	if strings.TrimSpace(dst.Image) == "" {
		dst.Image = src.Image
	}
	if strings.TrimSpace(dst.Favicon) == "" {
		dst.Favicon = src.Favicon
	}
	return dst
}
