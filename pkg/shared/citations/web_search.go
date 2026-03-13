package citations

import (
	"net/url"

	"github.com/beeper/agentremote/pkg/shared/websearch"
)

// ExtractWebSearchCitations parses a JSON tool output containing web search results
// and returns the extracted source citations. The output is expected to be a JSON object
// with a "results" array of objects containing url, title, description, etc.
func ExtractWebSearchCitations(output string) []SourceCitation {
	results := websearch.ResultsFromJSON(output)
	if len(results) == 0 {
		return nil
	}

	result := make([]SourceCitation, 0, len(results))
	for _, entry := range results {
		urlStr := entry.URL
		if urlStr == "" {
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
		result = append(result, SourceCitation{
			URL:         urlStr,
			Title:       entry.Title,
			Description: entry.Description,
			Published:   entry.Published,
			SiteName:    entry.SiteName,
			Author:      entry.Author,
			Image:       entry.Image,
			Favicon:     entry.Favicon,
		})
	}
	return result
}
