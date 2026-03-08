package opencodebridge

import (
	"strings"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
	"github.com/beeper/ai-bridge/pkg/shared/maputil"
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

// CanonicalReasoningText extracts and joins all reasoning-type text from a canonical UI message.
func CanonicalReasoningText(uiMessage map[string]any) string {
	parts, _ := uiMessage["parts"].([]any)
	var sb strings.Builder
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok || maputil.StringArg(part, "type") != "reasoning" {
			continue
		}
		text := maputil.StringArg(part, "text")
		if text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(text)
	}
	return sb.String()
}

// CanonicalGeneratedFiles extracts file references from a canonical UI message.
func CanonicalGeneratedFiles(uiMessage map[string]any) []bridgeadapter.GeneratedFileRef {
	parts, _ := uiMessage["parts"].([]any)
	var refs []bridgeadapter.GeneratedFileRef
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok || maputil.StringArg(part, "type") != "file" {
			continue
		}
		url := maputil.StringArg(part, "url")
		if url == "" {
			continue
		}
		refs = append(refs, bridgeadapter.GeneratedFileRef{
			URL:      url,
			MimeType: stringutil.FirstNonEmpty(maputil.StringArg(part, "mediaType"), "application/octet-stream"),
		})
	}
	return refs
}

// CanonicalToolCalls extracts tool call metadata from a canonical UI message.
func CanonicalToolCalls(uiMessage map[string]any) []bridgeadapter.ToolCallMetadata {
	parts, _ := uiMessage["parts"].([]any)
	var calls []bridgeadapter.ToolCallMetadata
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok || maputil.StringArg(part, "type") != "dynamic-tool" {
			continue
		}
		call := bridgeadapter.ToolCallMetadata{
			CallID:   maputil.StringArg(part, "toolCallId"),
			ToolName: maputil.StringArg(part, "toolName"),
			ToolType: "opencode",
			Status:   maputil.StringArg(part, "state"),
		}
		if input, ok := part["input"].(map[string]any); ok {
			call.Input = input
		}
		if output, ok := part["output"].(map[string]any); ok {
			call.Output = output
		} else if text := maputil.StringArg(part, "output"); text != "" {
			call.Output = map[string]any{"text": text}
		}
		switch call.Status {
		case "output-available":
			call.ResultStatus = "completed"
		case "output-denied":
			call.ResultStatus = "denied"
		case "output-error":
			call.ResultStatus = "error"
			call.ErrorMessage = maputil.StringArg(part, "errorText")
		case "approval-requested":
			call.ResultStatus = "pending_approval"
		default:
			call.ResultStatus = call.Status
		}
		if call.CallID != "" {
			calls = append(calls, call)
		}
	}
	return calls
}
