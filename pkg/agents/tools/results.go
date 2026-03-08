package tools

import (
	"encoding/json"
	"fmt"

	"github.com/beeper/ai-bridge/pkg/shared/jsonutil"
)

// JSONResult creates a structured JSON result from any payload.
// Following clawdbot's jsonResult pattern.
func JSONResult(payload any) *Result {
	return &Result{
		Status:  ResultSuccess,
		Content: []ContentBlock{{Type: "text", Text: mustJSON(payload)}},
		Details: jsonutil.ToMap(payload),
	}
}

// ErrorResult creates an error result.
// Follows clawdbot pattern: don't throw, return structured errors.
func ErrorResult(toolName, message string) *Result {
	return &Result{
		Status:  ResultError,
		Content: []ContentBlock{{Type: "text", Text: message}},
		Details: map[string]any{"tool": toolName, "error": message},
		Error:   message,
	}
}

// mustJSON marshals payload to JSON, returning error message on failure.
func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error":"failed to marshal: %s"}`, err)
	}
	return string(data)
}
