package exa

import (
	"fmt"
	"net/url"
	"strings"
)

// DefaultBaseURL is the standard Exa API endpoint.
const DefaultBaseURL = "https://api.exa.ai"

// AuthHeaders returns the standard Exa API authentication headers.
// For non-Exa base URLs, it also attaches a Bearer Authorization header.
func AuthHeaders(baseURL, apiKey string) map[string]string {
	headers := map[string]string{
		"x-api-key": apiKey,
		"accept":    "application/json",
	}
	if ShouldAttachBearerAuth(baseURL) {
		headers["Authorization"] = fmt.Sprintf("Bearer %s", apiKey)
	}
	return headers
}

// ShouldAttachBearerAuth returns true when the base URL is a non-Exa proxy
// that requires a standard Bearer token in addition to the x-api-key header.
func ShouldAttachBearerAuth(baseURL string) bool {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Hostname() == "" {
		return true
	}
	return !strings.EqualFold(parsed.Hostname(), "api.exa.ai")
}
