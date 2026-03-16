package ai

import (
	"encoding/json"
	"fmt"
)

func jsonActionResult(action string, fields map[string]any) (string, error) {
	payload := map[string]any{
		"action": action,
	}
	for k, v := range fields {
		payload[k] = v
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to build response: %w", err)
	}
	return string(data), nil
}
