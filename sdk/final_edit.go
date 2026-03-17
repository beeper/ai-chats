package sdk

import (
	"strings"

	"github.com/beeper/agentremote/pkg/matrixevents"
)

// BuildCompactFinalUIMessage removes streaming-only parts from a UI message so
// the payload is suitable for attachment to the final Matrix edit.
func BuildCompactFinalUIMessage(uiMessage map[string]any) map[string]any {
	if len(uiMessage) == 0 {
		return nil
	}
	out := map[string]any{}
	for key, value := range uiMessage {
		if key != "parts" {
			out[key] = value
		}
	}

	var rawParts []any
	switch typed := uiMessage["parts"].(type) {
	case []any:
		rawParts = typed
	case []map[string]any:
		rawParts = make([]any, 0, len(typed))
		for _, part := range typed {
			rawParts = append(rawParts, part)
		}
	}
	if len(rawParts) == 0 {
		return out
	}

	parts := make([]any, 0, len(rawParts))
	for _, raw := range rawParts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(stringValue(part["type"])) {
		case "text", "reasoning", "step-start":
			continue
		default:
			parts = append(parts, part)
		}
	}
	if len(parts) > 0 {
		out["parts"] = append([]any(nil), parts...)
	}
	return out
}

// BuildDefaultFinalEditTopLevelExtra builds the SDK's default metadata payload
// for terminal final edits.
func BuildDefaultFinalEditTopLevelExtra(uiMessage map[string]any) map[string]any {
	extra := map[string]any{
		"com.beeper.dont_render_edited": true,
		"m.mentions":                    map[string]any{},
	}
	if len(uiMessage) > 0 {
		extra[matrixevents.BeeperAIKey] = uiMessage
	}
	return extra
}

func hasMeaningfulFinalUIMessage(uiMessage map[string]any) bool {
	if len(uiMessage) == 0 {
		return false
	}
	for key, value := range uiMessage {
		switch key {
		case "id", "role":
			continue
		case "parts":
			switch typed := value.(type) {
			case []any:
				if len(typed) > 0 {
					return true
				}
			case []map[string]any:
				if len(typed) > 0 {
					return true
				}
			}
		case "metadata":
			if typed, ok := value.(map[string]any); ok {
				if len(typed) > 0 {
					return true
				}
				continue
			}
			if value != nil {
				return true
			}
		default:
			if value != nil {
				return true
			}
		}
	}
	return false
}
