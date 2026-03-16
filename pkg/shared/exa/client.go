package exa

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/httputil"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

// Enabled returns true when the Exa provider is enabled and has credentials.
func Enabled(enabled *bool, apiKey string) bool {
	return stringutil.BoolPtrOr(enabled, true) && strings.TrimSpace(apiKey) != ""
}

// Endpoint resolves an Exa API endpoint path against the configured base URL.
func Endpoint(baseURL, path string) (string, error) {
	base := stringutil.NormalizeBaseURL(baseURL)
	if base == "" {
		return "", errors.New("exa base_url is empty")
	}
	return base + path, nil
}

// PostJSON sends a JSON request to the configured Exa endpoint with standard auth headers.
func PostJSON(ctx context.Context, baseURL, path, apiKey string, payload any, timeoutSecs int) ([]byte, error) {
	endpoint, err := Endpoint(baseURL, path)
	if err != nil {
		return nil, err
	}
	data, _, err := httputil.PostJSON(ctx, endpoint, AuthHeaders(baseURL, apiKey), payload, timeoutSecs)
	return data, err
}

// PostAndDecodeJSON sends a JSON request and decodes the JSON response into out.
func PostAndDecodeJSON(ctx context.Context, baseURL, path, apiKey string, payload any, timeoutSecs int, out any) error {
	data, err := PostJSON(ctx, baseURL, path, apiKey, payload, timeoutSecs)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// ApplyEnv fills empty Exa credentials from standard environment variables.
func ApplyEnv(apiKey, baseURL *string) {
	if apiKey != nil {
		*apiKey = stringutil.EnvOr(*apiKey, os.Getenv("EXA_API_KEY"))
	}
	if baseURL != nil {
		*baseURL = stringutil.EnvOr(*baseURL, os.Getenv("EXA_BASE_URL"))
	}
}
