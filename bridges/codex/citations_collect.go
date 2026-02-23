package codex

import (
	"encoding/json"
	"strings"
)

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

func extractWebSearchCitationsFromToolOutput(toolName, output string) []sourceCitation {
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
	citations := make([]sourceCitation, 0, len(rawResults))
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
		title, _ := m["title"].(string)
		description, _ := m["description"].(string)
		siteName, _ := m["siteName"].(string)
		citations = append(citations, sourceCitation{
			URL:         url,
			Title:       strings.TrimSpace(title),
			Description: strings.TrimSpace(description),
			SiteName:    strings.TrimSpace(siteName),
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
	for _, citation := range append(existing, incoming...) {
		url := strings.TrimSpace(citation.URL)
		if url == "" {
			continue
		}
		if idx, ok := seen[url]; ok {
			merged[idx] = mergeCitationFields(merged[idx], citation)
			continue
		}
		seen[url] = len(merged)
		merged = append(merged, citation)
	}
	return merged
}

func mergeCitationFields(base, incoming sourceCitation) sourceCitation {
	if strings.TrimSpace(base.Title) == "" {
		base.Title = incoming.Title
	}
	if strings.TrimSpace(base.Description) == "" {
		base.Description = incoming.Description
	}
	if strings.TrimSpace(base.Published) == "" {
		base.Published = incoming.Published
	}
	if strings.TrimSpace(base.SiteName) == "" {
		base.SiteName = incoming.SiteName
	}
	if strings.TrimSpace(base.Author) == "" {
		base.Author = incoming.Author
	}
	if strings.TrimSpace(base.Image) == "" {
		base.Image = incoming.Image
	}
	if strings.TrimSpace(base.Favicon) == "" {
		base.Favicon = incoming.Favicon
	}
	return base
}
