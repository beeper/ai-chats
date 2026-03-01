package codex

import (
	"encoding/json"
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
