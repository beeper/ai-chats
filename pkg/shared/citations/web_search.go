package citations

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/maputil"
)

// ExtractWebSearchCitations parses a JSON tool output containing web search results
// and returns the extracted source citations. The output is expected to be a JSON object
// with a "results" array of objects containing url, title, description, etc.
func ExtractWebSearchCitations(output string) []SourceCitation {
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

	result := make([]SourceCitation, 0, len(rawResults))
	for _, rawResult := range rawResults {
		entry, ok := rawResult.(map[string]any)
		if !ok {
			continue
		}
		urlStr := maputil.StringArg(entry, "url")
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
			Title:       maputil.StringArg(entry, "title"),
			Description: maputil.StringArg(entry, "description"),
			Published:   maputil.StringArg(entry, "published"),
			SiteName:    maputil.StringArg(entry, "siteName"),
			Author:      maputil.StringArg(entry, "author"),
			Image:       maputil.StringArg(entry, "image"),
			Favicon:     maputil.StringArg(entry, "favicon"),
		})
	}
	return result
}
