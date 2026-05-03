package toolspec

const (
	WebSearchName        = "web_search"
	WebSearchDescription = "Search the web with Exa and return titles, URLs, snippets, and optional highlights."

	WebFetchName        = "web_fetch"
	WebFetchDescription = "Fetch readable content from a URL using the configured direct or Exa fetch provider."
)

func WebSearchSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"query": map[string]any{
			"type":        "string",
			"description": "Search query.",
		},
		"num_results": map[string]any{
			"type":        "integer",
			"description": "Maximum number of results to return.",
			"minimum":     1,
			"maximum":     10,
		},
	}, "query")
}

func WebFetchSchema() map[string]any {
	return ObjectSchema(map[string]any{
		"url": map[string]any{
			"type":        "string",
			"description": "URL to fetch.",
		},
		"max_chars": map[string]any{
			"type":        "integer",
			"description": "Maximum number of text characters to return.",
			"minimum":     1,
		},
	}, "url")
}

func ObjectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		values := make([]any, 0, len(required))
		for _, field := range required {
			values = append(values, field)
		}
		schema["required"] = values
	}
	return schema
}
