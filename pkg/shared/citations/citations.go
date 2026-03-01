// Package citations provides shared citation and document types and helper
// functions used by both the connector and bridge packages.
package citations

import (
	"fmt"
	"strings"
)

// SourceCitation represents a URL citation extracted from AI tool output.
type SourceCitation struct {
	URL         string
	Title       string
	Description string
	Published   string
	SiteName    string
	Author      string
	Image       string
	Favicon     string
}

// SourceDocument represents a file/document citation.
type SourceDocument struct {
	ID        string
	Title     string
	Filename  string
	MediaType string
}

// GeneratedFilePart pairs a URL with its media type for generated files.
type GeneratedFilePart struct {
	URL       string
	MediaType string
}

// ProviderMetadata builds the providerMetadata map for a source-url part from
// a SourceCitation. The keys match what the desktop client reads (e.g.
// "siteName" in camelCase).
func ProviderMetadata(c SourceCitation) map[string]any {
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

// MergeCitationFields fills empty fields of dst from src.
func MergeCitationFields(dst, src SourceCitation) SourceCitation {
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

// MergeSourceCitations deduplicates citations by URL, merging fields when the
// same URL appears more than once.
func MergeSourceCitations(existing, incoming []SourceCitation) []SourceCitation {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]int, len(existing)+len(incoming))
	merged := make([]SourceCitation, 0, len(existing)+len(incoming))
	for _, citation := range existing {
		url := strings.TrimSpace(citation.URL)
		if url == "" {
			continue
		}
		if idx, ok := seen[url]; ok {
			merged[idx] = MergeCitationFields(merged[idx], citation)
			continue
		}
		seen[url] = len(merged)
		merged = append(merged, citation)
	}
	for _, citation := range incoming {
		url := strings.TrimSpace(citation.URL)
		if url == "" {
			continue
		}
		if idx, ok := seen[url]; ok {
			merged[idx] = MergeCitationFields(merged[idx], citation)
			continue
		}
		seen[url] = len(merged)
		merged = append(merged, citation)
	}
	return merged
}

// BuildSourceParts converts citations and documents into stream-event source
// parts. This is the base version without link-preview enrichment; callers
// needing preview data should use the connector-specific variant.
func BuildSourceParts(citations []SourceCitation, documents []SourceDocument) []map[string]any {
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
		seenKey := "url:" + url
		if _, ok := seen[seenKey]; ok {
			continue
		}
		seen[seenKey] = struct{}{}
		p := map[string]any{
			"type":     "source-url",
			"sourceId": fmt.Sprintf("source-%d", len(parts)+1),
			"url":      url,
		}
		if title := strings.TrimSpace(c.Title); title != "" {
			p["title"] = title
		}
		if meta := ProviderMetadata(c); len(meta) > 0 {
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
		seenKey := "doc:" + key
		if _, ok := seen[seenKey]; ok {
			continue
		}
		seen[seenKey] = struct{}{}
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

// GeneratedFilesToParts converts generated files into stream-event parts.
func GeneratedFilesToParts(files []GeneratedFilePart) []map[string]any {
	if len(files) == 0 {
		return nil
	}
	parts := make([]map[string]any, 0, len(files))
	for _, file := range files {
		url := strings.TrimSpace(file.URL)
		if url == "" {
			continue
		}
		parts = append(parts, map[string]any{
			"type":      "file",
			"url":       url,
			"mediaType": strings.TrimSpace(file.MediaType),
		})
	}
	return parts
}
