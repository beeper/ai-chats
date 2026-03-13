package agentremote

import (
	"strings"

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
	"github.com/beeper/agentremote/pkg/shared/maputil"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

// NormalizeUIParts coerces a raw parts value (which may be []any or
// []map[string]any) into a typed []map[string]any slice.
func NormalizeUIParts(raw any) []map[string]any {
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			part := jsonutil.ToMap(item)
			if len(part) == 0 {
				continue
			}
			out = append(out, part)
		}
		return out
	default:
		return nil
	}
}

// CanonicalReasoningText extracts and joins all reasoning-type text from
// a canonical UI message parts slice.
func CanonicalReasoningText(parts []map[string]any) string {
	var sb strings.Builder
	for _, part := range parts {
		if maputil.StringArg(part, "type") != "reasoning" {
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

// CanonicalGeneratedFiles extracts file references from a canonical UI
// message parts slice.
func CanonicalGeneratedFiles(parts []map[string]any) []GeneratedFileRef {
	var refs []GeneratedFileRef
	for _, part := range parts {
		if maputil.StringArg(part, "type") != "file" {
			continue
		}
		url := maputil.StringArg(part, "url")
		if url == "" {
			continue
		}
		refs = append(refs, GeneratedFileRef{
			URL:      url,
			MimeType: stringutil.FirstNonEmpty(maputil.StringArg(part, "mediaType"), "application/octet-stream"),
		})
	}
	return refs
}

// CanonicalToolCalls extracts tool call metadata from a canonical UI message
// parts slice. toolType identifies the bridge (e.g. "opencode", "openclaw").
func CanonicalToolCalls(parts []map[string]any, toolType string) []ToolCallMetadata {
	var calls []ToolCallMetadata
	for _, part := range parts {
		if maputil.StringArg(part, "type") != "dynamic-tool" {
			continue
		}
		call := ToolCallMetadata{
			CallID:   maputil.StringArg(part, "toolCallId"),
			ToolName: maputil.StringArg(part, "toolName"),
			ToolType: toolType,
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
