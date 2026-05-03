package citations

import (
	"net/url"

	"github.com/beeper/ai-chats/pkg/shared/websearch"
)

// ExtractWebSearchCitations parses a JSON tool output containing web search results
// and returns the extracted source citations. The output is expected to be a JSON object
// with a "results" array of objects containing url, title, description, etc.
func ExtractWebSearchCitations(output string) []SourceCitation {
	results := websearch.ResultsFromJSON(output)
	if len(results) == 0 {
		return nil
	}

	citations := make([]SourceCitation, 0, len(results))
	for _, r := range results {
		if r.URL == "" {
			continue
		}
		parsed, err := url.Parse(r.URL)
		if err != nil {
			continue
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			continue
		}
		citations = append(citations, SourceCitation{
			URL:         r.URL,
			Title:       r.Title,
			Description: r.Description,
			Published:   r.Published,
			SiteName:    r.SiteName,
			Author:      r.Author,
			Image:       r.Image,
			Favicon:     r.Favicon,
		})
	}
	return citations
}
