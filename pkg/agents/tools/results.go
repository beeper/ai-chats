package tools

import (
	"encoding/json"
	"fmt"

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

// JSONResult creates a structured JSON result from any payload.
func JSONResult(payload any) *Result {
	return &Result{
		Status:  ResultSuccess,
		Content: []ContentBlock{{Type: "text", Text: mustJSON(payload)}},
		Details: jsonutil.ToMap(payload),
	}
}

// ErrorResult creates an error result with structured metadata.
func ErrorResult(toolName, message string) *Result {
	return &Result{
		Status:  ResultError,
		Content: []ContentBlock{{Type: "text", Text: message}},
		Details: map[string]any{"tool": toolName, "error": message},
		Error:   message,
	}
}

// JSONErrorResult creates a successful JSON payload whose body includes a
// status=error marker. Use this when a tool contract expects structured JSON
// output even for recoverable/user-facing failures.
func JSONErrorResult(message string) *Result {
	return JSONResult(map[string]any{
		"status": "error",
		"error":  message,
	})
}

// mustJSON marshals payload to JSON, returning error message on failure.
func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		errMsg, _ := json.Marshal(fmt.Sprintf("failed to marshal: %s", err))
		return fmt.Sprintf(`{"error":%s}`, errMsg)
	}
	return string(data)
}
