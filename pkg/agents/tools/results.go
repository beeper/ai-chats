package tools

import (
	"encoding/json"
	"fmt"
)

// JSONResult creates a structured JSON result from any payload.
// Following clawdbot's jsonResult pattern.
func JSONResult(payload any) *Result {
	return &Result{
		Status:  ResultSuccess,
		Content: []ContentBlock{{Type: "text", Text: mustJSON(payload)}},
		Details: toMap(payload),
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
		return fmt.Sprintf(`{"error":"failed to marshal: %s"}`, err.Error())
	}
	return string(data)
}

// toMap converts a struct to map[string]any for Details field.
func toMap(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

// IsSuccess returns true if the result indicates success.
func (r *Result) IsSuccess() bool {
	return r.Status == ResultSuccess
}

// IsError returns true if the result indicates an error.
func (r *Result) IsError() bool {
	return r.Status == ResultError
}

// HasImages returns true if the result contains image blocks.
func (r *Result) HasImages() bool {
	for _, block := range r.Content {
		if block.Type == "image" {
			return true
		}
	}
	return false
}
