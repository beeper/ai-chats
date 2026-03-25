package streamui

import (
	"encoding/json"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

func ApplyChunk(state *UIState, chunk map[string]any) {
	if state == nil || len(chunk) == 0 {
		return
	}
	state.InitMaps()
	typ := stringutil.TrimString(chunk["type"])
	if typ == "" {
		return
	}

	switch typ {
	case "start":
		msg := ensureAssistantMessage(state)
		if messageID := stringutil.TrimString(chunk["messageId"]); messageID != "" {
			msg["id"] = messageID
		}
		mergeMessageMetadata(msg, chunk["messageMetadata"])
	case "message-metadata":
		mergeMessageMetadata(ensureAssistantMessage(state), chunk["messageMetadata"])
	case "start-step":
		appendPart(state, map[string]any{"type": "step-start"})
	case "finish-step":
		// Stream-only marker; step-start is the persisted boundary.
	case "text-start":
		partID := stringutil.TrimString(chunk["id"])
		if partID == "" {
			return
		}
		state.UITextPartIndexByID[partID] = appendPart(state, newStreamingTextPart("text", jsonutil.DeepCloneMap(jsonutil.ToMap(chunk["providerMetadata"]))))
	case "text-delta":
		partID := stringutil.TrimString(chunk["id"])
		if partID == "" {
			return
		}
		part := ensureTextPart(state, partID, jsonutil.DeepCloneMap(jsonutil.ToMap(chunk["providerMetadata"])))
		part["state"] = "streaming"
		part["text"] = stringutil.StringValue(part["text"]) + stringutil.StringValue(chunk["delta"])
	case "text-end":
		partID := stringutil.TrimString(chunk["id"])
		if partID == "" {
			return
		}
		part := ensureTextPart(state, partID, nil)
		part["state"] = "done"
		delete(state.UITextPartIndexByID, partID)
	case "reasoning-start":
		partID := stringutil.TrimString(chunk["id"])
		if partID == "" {
			return
		}
		state.UIReasoningPartIndexByID[partID] = appendPart(state, newStreamingTextPart("reasoning", jsonutil.DeepCloneMap(jsonutil.ToMap(chunk["providerMetadata"]))))
	case "reasoning-delta":
		partID := stringutil.TrimString(chunk["id"])
		if partID == "" {
			return
		}
		part := ensureReasoningPart(state, partID, jsonutil.DeepCloneMap(jsonutil.ToMap(chunk["providerMetadata"])))
		part["state"] = "streaming"
		part["text"] = stringutil.StringValue(part["text"]) + stringutil.StringValue(chunk["delta"])
	case "reasoning-end":
		partID := stringutil.TrimString(chunk["id"])
		if partID == "" {
			return
		}
		part := ensureReasoningPart(state, partID, nil)
		part["state"] = "done"
		delete(state.UIReasoningPartIndexByID, partID)
	case "tool-input-start":
		toolCallID := stringutil.TrimString(chunk["toolCallId"])
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, stringutil.TrimString(chunk["toolName"]))
		part["state"] = "input-streaming"
		part["input"] = ""
		applyToolInputMeta(part, chunk)
	case "tool-input-delta":
		toolCallID := stringutil.TrimString(chunk["toolCallId"])
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, stringutil.TrimString(state.UIToolNameByToolCallID[toolCallID]))
		part["state"] = "input-streaming"
		accumulated := state.UIToolInputTextByID[toolCallID] + stringutil.StringValue(chunk["inputTextDelta"])
		state.UIToolInputTextByID[toolCallID] = accumulated
		if parsed, ok := tryJSON(accumulated); ok {
			part["input"] = parsed
		} else {
			part["input"] = accumulated
		}
	case "tool-input-available":
		toolCallID := stringutil.TrimString(chunk["toolCallId"])
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, stringutil.TrimString(chunk["toolName"]))
		part["state"] = "input-available"
		part["input"] = jsonutil.DeepCloneAny(chunk["input"])
		applyToolInputMeta(part, chunk)
	case "tool-input-error":
		toolCallID := stringutil.TrimString(chunk["toolCallId"])
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, stringutil.TrimString(chunk["toolName"]))
		part["state"] = "output-error"
		part["input"] = jsonutil.DeepCloneAny(chunk["input"])
		part["errorText"] = stringutil.StringValue(chunk["errorText"])
		applyToolInputMeta(part, chunk)
	case "tool-approval-request":
		toolCallID := stringutil.TrimString(chunk["toolCallId"])
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, stringutil.TrimString(state.UIToolNameByToolCallID[toolCallID]))
		part["state"] = "approval-requested"
		part["approval"] = map[string]any{"id": stringutil.TrimString(chunk["approvalId"])}
	case "tool-approval-response":
		RecordApprovalResponse(
			state,
			stringutil.TrimString(chunk["approvalId"]),
			stringutil.TrimString(chunk["toolCallId"]),
			boolValueOrDefault(chunk["approved"], false),
			stringutil.TrimString(chunk["reason"]),
		)
	case "tool-output-available":
		toolCallID := stringutil.TrimString(chunk["toolCallId"])
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, stringutil.TrimString(state.UIToolNameByToolCallID[toolCallID]))
		part["state"] = "output-available"
		part["output"] = jsonutil.DeepCloneAny(chunk["output"])
		if providerExecuted, ok := boolValue(chunk["providerExecuted"]); ok {
			part["providerExecuted"] = providerExecuted
		}
		if preliminary, ok := boolValue(chunk["preliminary"]); ok {
			part["preliminary"] = preliminary
		} else {
			delete(part, "preliminary")
		}
	case "tool-output-error":
		toolCallID := stringutil.TrimString(chunk["toolCallId"])
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, stringutil.TrimString(state.UIToolNameByToolCallID[toolCallID]))
		part["state"] = "output-error"
		part["errorText"] = stringutil.StringValue(chunk["errorText"])
		if providerExecuted, ok := boolValue(chunk["providerExecuted"]); ok {
			part["providerExecuted"] = providerExecuted
		}
	case "tool-output-denied":
		toolCallID := stringutil.TrimString(chunk["toolCallId"])
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, stringutil.TrimString(state.UIToolNameByToolCallID[toolCallID]))
		part["state"] = "output-denied"
	case "source-url", "source-document", "file":
		appendPart(state, jsonutil.DeepCloneMap(jsonutil.ToMap(chunk)))
	case "finish":
		mergeMessageMetadata(ensureAssistantMessage(state), chunk["messageMetadata"])
	case "error":
		setTerminalState(ensureAssistantMessage(state), "error", stringutil.StringValue(chunk["errorText"]))
	case "abort":
		setTerminalState(ensureAssistantMessage(state), "abort", stringutil.TrimString(chunk["reason"]))
	default:
		if strings.HasPrefix(typ, "data-") {
			if transient, ok := boolValue(chunk["transient"]); ok && transient {
				return
			}
			appendOrReplaceDataPart(state, jsonutil.DeepCloneMap(jsonutil.ToMap(chunk)))
		}
	}
}

func SnapshotUIMessage(state *UIState) map[string]any {
	if state == nil || len(state.UIMessage) == 0 {
		return nil
	}
	return jsonutil.DeepCloneMap(jsonutil.ToMap(state.UIMessage))
}

func RecordApprovalResponse(state *UIState, approvalID, toolCallID string, approved bool, reason string) {
	if state == nil {
		return
	}
	state.InitMaps()
	approvalID = strings.TrimSpace(approvalID)
	toolCallID = strings.TrimSpace(toolCallID)
	if approvalID == "" {
		return
	}
	if toolCallID == "" {
		toolCallID = strings.TrimSpace(state.UIToolCallIDByApproval[approvalID])
	}
	if toolCallID == "" {
		return
	}
	part := ensureToolPart(state, toolCallID, strings.TrimSpace(state.UIToolNameByToolCallID[toolCallID]))
	part["state"] = "approval-responded"
	approval := map[string]any{
		"id":       approvalID,
		"approved": approved,
	}
	if trimmedReason := strings.TrimSpace(reason); trimmedReason != "" {
		approval["reason"] = trimmedReason
	}
	part["approval"] = approval
}

func ensureAssistantMessage(state *UIState) map[string]any {
	if state.UIMessage == nil {
		state.UIMessage = map[string]any{
			"id":    state.TurnID,
			"role":  "assistant",
			"parts": []any{},
		}
	}
	if stringutil.TrimString(state.UIMessage["id"]) == "" {
		state.UIMessage["id"] = state.TurnID
	}
	if stringutil.TrimString(state.UIMessage["role"]) == "" {
		state.UIMessage["role"] = "assistant"
	}
	if _, ok := state.UIMessage["parts"].([]any); !ok {
		state.UIMessage["parts"] = []any{}
	}
	return state.UIMessage
}

func appendPart(state *UIState, part map[string]any) int {
	msg := ensureAssistantMessage(state)
	parts, _ := msg["parts"].([]any)
	idx := len(parts)
	msg["parts"] = append(parts, part)
	return idx
}

func ensureStreamingPart(state *UIState, indexMap map[string]int, partID, partType string, providerMetadata map[string]any) map[string]any {
	if idx, ok := indexMap[partID]; ok {
		return getPartAt(state, idx)
	}
	part := newStreamingTextPart(partType, providerMetadata)
	indexMap[partID] = appendPart(state, part)
	return part
}

func ensureTextPart(state *UIState, partID string, providerMetadata map[string]any) map[string]any {
	return ensureStreamingPart(state, state.UITextPartIndexByID, partID, "text", providerMetadata)
}

func ensureReasoningPart(state *UIState, partID string, providerMetadata map[string]any) map[string]any {
	return ensureStreamingPart(state, state.UIReasoningPartIndexByID, partID, "reasoning", providerMetadata)
}

func newStreamingTextPart(partType string, providerMetadata map[string]any) map[string]any {
	part := map[string]any{
		"type":  partType,
		"text":  "",
		"state": "streaming",
	}
	if len(providerMetadata) > 0 {
		part["providerMetadata"] = providerMetadata
	}
	return part
}

func ensureToolPart(state *UIState, toolCallID string, toolName string) map[string]any {
	if idx, ok := state.UIToolPartIndexByID[toolCallID]; ok {
		part := getPartAt(state, idx)
		if toolName != "" {
			part["toolName"] = toolName
		}
		return part
	}
	if toolName == "" {
		toolName = "tool"
	}
	part := map[string]any{
		"type":       "dynamic-tool",
		"toolName":   toolName,
		"toolCallId": toolCallID,
		"state":      "input-streaming",
		"input":      "",
	}
	state.UIToolPartIndexByID[toolCallID] = appendPart(state, part)
	state.UIToolNameByToolCallID[toolCallID] = toolName
	return part
}

func getPartAt(state *UIState, idx int) map[string]any {
	msg := ensureAssistantMessage(state)
	parts, _ := msg["parts"].([]any)
	if idx < 0 || idx >= len(parts) {
		return map[string]any{}
	}
	part, _ := parts[idx].(map[string]any)
	return part
}

func appendOrReplaceDataPart(state *UIState, part map[string]any) {
	msg := ensureAssistantMessage(state)
	parts, _ := msg["parts"].([]any)
	partType := stringutil.TrimString(part["type"])
	partID := stringutil.TrimString(part["id"])
	if partID != "" {
		for idx, raw := range parts {
			existing, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if stringutil.TrimString(existing["type"]) == partType && stringutil.TrimString(existing["id"]) == partID {
				parts[idx] = part
				msg["parts"] = parts
				return
			}
		}
	}
	msg["parts"] = append(parts, part)
}

func mergeMessageMetadata(message map[string]any, raw any) {
	if message == nil {
		return
	}
	next := jsonutil.ToMap(raw)
	if len(next) == 0 {
		return
	}
	existing, _ := message["metadata"].(map[string]any)
	if len(existing) == 0 {
		message["metadata"] = next
		return
	}
	message["metadata"] = jsonutil.MergeRecursive(existing, next)
}

func setTerminalState(message map[string]any, typ string, reason string) {
	if message == nil {
		return
	}
	metadata, _ := message["metadata"].(map[string]any)
	if len(metadata) == 0 {
		metadata = map[string]any{}
	}
	terminal := map[string]any{"type": typ}
	if reason = strings.TrimSpace(reason); reason != "" && typ == "error" {
		terminal["errorText"] = reason
	}
	metadata["beeper_terminal_state"] = terminal
	message["metadata"] = metadata
}

func applyToolInputMeta(part, chunk map[string]any) {
	if title := stringutil.TrimString(chunk["title"]); title != "" {
		part["title"] = title
	}
	if providerExecuted, ok := boolValue(chunk["providerExecuted"]); ok {
		part["providerExecuted"] = providerExecuted
	}
	if providerMetadata := jsonutil.DeepCloneMap(jsonutil.ToMap(chunk["providerMetadata"])); len(providerMetadata) > 0 {
		part["callProviderMetadata"] = providerMetadata
	}
}

func boolValue(raw any) (bool, bool) {
	value, ok := raw.(bool)
	return value, ok
}

func boolValueOrDefault(raw any, fallback bool) bool {
	if value, ok := boolValue(raw); ok {
		return value
	}
	return fallback
}

func tryJSON(raw string) (any, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, false
	}
	return value, true
}
