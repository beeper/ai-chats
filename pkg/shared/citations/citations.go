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
// "siteName" in camelCase). Emit both siteName and site_name during transition.
func ProviderMetadata(c SourceCitation) map[string]any {
	meta := map[string]any{}
	setIfNonEmpty := func(key, val string) {
		if v := strings.TrimSpace(val); v != "" {
			meta[key] = v
		}
	}
	setIfNonEmpty("description", c.Description)
	setIfNonEmpty("published", c.Published)
	if v := strings.TrimSpace(c.SiteName); v != "" {
		meta["siteName"] = v
		meta["site_name"] = v
	}
	setIfNonEmpty("author", c.Author)
	setIfNonEmpty("image", c.Image)
	setIfNonEmpty("favicon", c.Favicon)
	if len(meta) == 0 {
		return nil
	}
	return meta
}

// MergeCitationFields fills empty fields of dst from src.
func MergeCitationFields(dst, src SourceCitation) SourceCitation {
	mergeField(&dst.Title, src.Title)
	mergeField(&dst.Description, src.Description)
	mergeField(&dst.Published, src.Published)
	mergeField(&dst.SiteName, src.SiteName)
	mergeField(&dst.Author, src.Author)
	mergeField(&dst.Image, src.Image)
	mergeField(&dst.Favicon, src.Favicon)
	return dst
}

// mergeField sets dst to src if dst is empty after trimming.
func mergeField(dst *string, src string) {
	if strings.TrimSpace(*dst) == "" {
		*dst = src
	}
}

// MergeSourceCitations deduplicates citations by URL, merging fields when the
// same URL appears more than once.
func MergeSourceCitations(existing, incoming []SourceCitation) []SourceCitation {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]int, len(existing)+len(incoming))
	// Allocate a fresh slice to avoid mutating the backing array of existing.
	merged := make([]SourceCitation, 0, len(existing)+len(incoming))
	addCitation := func(citation SourceCitation) {
		url := strings.TrimSpace(citation.URL)
		if url == "" {
			return
		}
		if idx, ok := seen[url]; ok {
			merged[idx] = MergeCitationFields(merged[idx], citation)
			return
		}
		seen[url] = len(merged)
		merged = append(merged, citation)
	}
	for _, c := range existing {
		addCitation(c)
	}
	for _, c := range incoming {
		addCitation(c)
	}
	return merged
}

// AppendUniqueCitation appends a single citation, deduplicating by URL without
// allocating a map. Use this on hot paths (e.g. streaming) where citations
// arrive one at a time.
func AppendUniqueCitation(citations []SourceCitation, c SourceCitation) []SourceCitation {
	url := strings.TrimSpace(c.URL)
	if url == "" {
		return citations
	}
	for i, existing := range citations {
		if strings.TrimSpace(existing.URL) == url {
			citations[i] = MergeCitationFields(existing, c)
			return citations
		}
	}
	return append(citations, c)
}

// AppendSourceURLPart appends a deduplicated source-url part to parts.
func AppendSourceURLPart(parts *[]map[string]any, seen map[string]struct{}, url, title string, providerMetadata map[string]any) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	seenKey := "url:" + url
	if _, ok := seen[seenKey]; ok {
		return
	}
	seen[seenKey] = struct{}{}
	part := map[string]any{
		"type":     "source-url",
		"sourceId": fmt.Sprintf("source-%d", len(*parts)+1),
		"url":      url,
	}
	if title = strings.TrimSpace(title); title != "" {
		part["title"] = title
	}
	if len(providerMetadata) > 0 {
		part["providerMetadata"] = providerMetadata
	}
	*parts = append(*parts, part)
}

// AppendSourceDocumentPart appends a deduplicated source-document part to parts.
func AppendSourceDocumentPart(parts *[]map[string]any, seen map[string]struct{}, doc SourceDocument) {
	key := sourceDocumentKey(doc)
	if key == "" {
		return
	}
	seenKey := "doc:" + key
	if _, ok := seen[seenKey]; ok {
		return
	}
	seen[seenKey] = struct{}{}
	part := map[string]any{
		"type":     "source-document",
		"sourceId": fmt.Sprintf("source-%d", len(*parts)+1),
	}
	if mediaType := strings.TrimSpace(doc.MediaType); mediaType != "" {
		part["mediaType"] = mediaType
	}
	if title := strings.TrimSpace(doc.Title); title != "" {
		part["title"] = title
	}
	if filename := strings.TrimSpace(doc.Filename); filename != "" {
		part["filename"] = filename
	}
	*parts = append(*parts, part)
}

func sourceDocumentKey(doc SourceDocument) string {
	for _, candidate := range []string{doc.ID, doc.Filename, doc.Title} {
		if key := strings.TrimSpace(candidate); key != "" {
			return key
		}
	}
	return ""
}
