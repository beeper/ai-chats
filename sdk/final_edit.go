package sdk

import (
	"maps"
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

	rawParts := extractUIMessageParts(uiMessage)
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

// BuildDefaultFinalEditExtra builds the SDK's default replacement payload
// that should live inside m.new_content for terminal final edits.
func BuildDefaultFinalEditExtra(uiMessage map[string]any) map[string]any {
	extra := map[string]any{}
	if len(uiMessage) > 0 {
		extra[matrixevents.BeeperAIKey] = uiMessage
	}
	return extra
}

// BuildDefaultFinalEditTopLevelExtra builds the SDK's edit-event-only metadata
// payload for terminal final edits.
func BuildDefaultFinalEditTopLevelExtra() map[string]any {
	return map[string]any{
		"com.beeper.dont_render_edited": true,
	}
}

func hasMeaningfulFinalUIMessage(uiMessage map[string]any) bool {
	if len(uiMessage) == 0 {
		return false
	}
	for key, value := range uiMessage {
		switch key {
		case "id", "role", "metadata":
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
		default:
			if value != nil {
				return true
			}
		}
	}
	return false
}

func withFinalEditFinishReason(uiMessage map[string]any, finishReason string) map[string]any {
	if len(uiMessage) == 0 || strings.TrimSpace(finishReason) == "" {
		return uiMessage
	}
	out := maps.Clone(uiMessage)
	metadata, _ := out["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	} else {
		metadata = maps.Clone(metadata)
	}
	if strings.TrimSpace(stringValue(metadata["finish_reason"])) == "" {
		metadata["finish_reason"] = strings.TrimSpace(finishReason)
	}
	out["metadata"] = metadata
	return out
}
