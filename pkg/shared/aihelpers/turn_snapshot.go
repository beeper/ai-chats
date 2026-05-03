package aihelpers

import (
	"strings"

	"github.com/beeper/ai-chats/pkg/shared/jsonutil"
)

func TurnText(td TurnData) string {
	var sb strings.Builder
	for _, part := range td.Parts {
		if normalizeTurnPartType(part.Type) != "text" || part.Text == "" {
			continue
		}
		sb.WriteString(part.Text)
	}
	return strings.TrimSpace(sb.String())
}

func TurnReasoningText(td TurnData) string {
	var texts []string
	for _, part := range td.Parts {
		if normalizeTurnPartType(part.Type) != "reasoning" {
			continue
		}
		text := strings.TrimSpace(part.Reasoning)
		if text == "" {
			text = strings.TrimSpace(part.Text)
		}
		if text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

func TurnGeneratedFiles(td TurnData) []GeneratedFileRef {
	var refs []GeneratedFileRef
	for _, part := range td.Parts {
		if normalizeTurnPartType(part.Type) != "file" || strings.TrimSpace(part.URL) == "" {
			continue
		}
		refs = append(refs, GeneratedFileRef{
			URL:      strings.TrimSpace(part.URL),
			MimeType: strings.TrimSpace(part.MediaType),
		})
	}
	return refs
}

func TurnToolCalls(td TurnData, defaultToolType string) []ToolCallMetadata {
	var calls []ToolCallMetadata
	for _, part := range td.Parts {
		if normalizeTurnPartType(part.Type) != "tool" {
			continue
		}
		callID := strings.TrimSpace(part.ToolCallID)
		if callID == "" {
			continue
		}
		call := ToolCallMetadata{
			CallID:       callID,
			ToolName:     strings.TrimSpace(part.ToolName),
			ToolType:     strings.TrimSpace(part.ToolType),
			Input:        canonicalJSONObject(part.Input),
			Output:       canonicalJSONObject(part.Output),
			Status:       strings.TrimSpace(part.State),
			ErrorMessage: strings.TrimSpace(part.ErrorText),
		}
		if call.ToolType == "" {
			call.ToolType = strings.TrimSpace(defaultToolType)
		}
		switch call.Status {
		case "output-available":
			call.ResultStatus = "completed"
		case "output-denied":
			call.ResultStatus = "denied"
		case "output-error":
			call.ResultStatus = "error"
		case "approval-requested":
			call.ResultStatus = "pending_approval"
		default:
			call.ResultStatus = call.Status
		}
		calls = append(calls, call)
	}
	return calls
}

func canonicalJSONObject(raw any) map[string]any {
	switch typed := jsonutil.DeepCloneAny(raw).(type) {
	case nil:
		return nil
	case map[string]any:
		return typed
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return map[string]any{"text": typed}
	default:
		return map[string]any{"value": typed}
	}
}
