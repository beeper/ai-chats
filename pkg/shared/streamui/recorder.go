package streamui

import (
	"encoding/json"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/jsonutil"
)

func ApplyChunk(state *UIState, chunk map[string]any) {
	if state == nil || len(chunk) == 0 {
		return
	}
	state.InitMaps()
	typ := strings.TrimSpace(stringValue(chunk["type"]))
	if typ == "" {
		return
	}

	switch typ {
	case "start":
		msg := ensureAssistantMessage(state)
		if messageID := strings.TrimSpace(stringValue(chunk["messageId"])); messageID != "" {
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
		partID := strings.TrimSpace(stringValue(chunk["id"]))
		if partID == "" {
			return
		}
		state.UITextPartIndexByID[partID] = appendPart(state, newStreamingTextPart("text", jsonutil.DeepCloneMap(jsonutil.ToMap(chunk["providerMetadata"]))))
	case "text-delta":
		partID := strings.TrimSpace(stringValue(chunk["id"]))
		if partID == "" {
			return
		}
		part := ensureTextPart(state, partID, jsonutil.DeepCloneMap(jsonutil.ToMap(chunk["providerMetadata"])))
		part["state"] = "streaming"
		part["text"] = stringValue(part["text"]) + stringValue(chunk["delta"])
	case "text-end":
		partID := strings.TrimSpace(stringValue(chunk["id"]))
		if partID == "" {
			return
		}
		part := ensureTextPart(state, partID, nil)
		part["state"] = "done"
		delete(state.UITextPartIndexByID, partID)
	case "reasoning-start":
		partID := strings.TrimSpace(stringValue(chunk["id"]))
		if partID == "" {
			return
		}
		state.UIReasoningPartIndexByID[partID] = appendPart(state, newStreamingTextPart("reasoning", jsonutil.DeepCloneMap(jsonutil.ToMap(chunk["providerMetadata"]))))
	case "reasoning-delta":
		partID := strings.TrimSpace(stringValue(chunk["id"]))
		if partID == "" {
			return
		}
		part := ensureReasoningPart(state, partID, jsonutil.DeepCloneMap(jsonutil.ToMap(chunk["providerMetadata"])))
		part["state"] = "streaming"
		part["text"] = stringValue(part["text"]) + stringValue(chunk["delta"])
	case "reasoning-end":
		partID := strings.TrimSpace(stringValue(chunk["id"]))
		if partID == "" {
			return
		}
		part := ensureReasoningPart(state, partID, nil)
		part["state"] = "done"
		delete(state.UIReasoningPartIndexByID, partID)
	case "tool-input-start":
		toolCallID := strings.TrimSpace(stringValue(chunk["toolCallId"]))
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, strings.TrimSpace(stringValue(chunk["toolName"])))
		part["state"] = "input-streaming"
		part["input"] = ""
		if title := strings.TrimSpace(stringValue(chunk["title"])); title != "" {
			part["title"] = title
		}
		if providerExecuted, ok := boolValue(chunk["providerExecuted"]); ok {
			part["providerExecuted"] = providerExecuted
		}
		if providerMetadata := jsonutil.DeepCloneMap(jsonutil.ToMap(chunk["providerMetadata"])); len(providerMetadata) > 0 {
			part["callProviderMetadata"] = providerMetadata
		}
	case "tool-input-delta":
		toolCallID := strings.TrimSpace(stringValue(chunk["toolCallId"]))
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, strings.TrimSpace(stringValue(state.UIToolNameByToolCallID[toolCallID])))
		part["state"] = "input-streaming"
		accumulated := state.UIToolInputTextByID[toolCallID] + stringValue(chunk["inputTextDelta"])
		state.UIToolInputTextByID[toolCallID] = accumulated
		if parsed, ok := tryJSON(accumulated); ok {
			part["input"] = parsed
		} else {
			part["input"] = accumulated
		}
	case "tool-input-available":
		toolCallID := strings.TrimSpace(stringValue(chunk["toolCallId"]))
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, strings.TrimSpace(stringValue(chunk["toolName"])))
		part["state"] = "input-available"
		part["input"] = jsonutil.DeepCloneAny(chunk["input"])
		if title := strings.TrimSpace(stringValue(chunk["title"])); title != "" {
			part["title"] = title
		}
		if providerExecuted, ok := boolValue(chunk["providerExecuted"]); ok {
			part["providerExecuted"] = providerExecuted
		}
		if providerMetadata := jsonutil.DeepCloneMap(jsonutil.ToMap(chunk["providerMetadata"])); len(providerMetadata) > 0 {
			part["callProviderMetadata"] = providerMetadata
		}
	case "tool-input-error":
		toolCallID := strings.TrimSpace(stringValue(chunk["toolCallId"]))
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, strings.TrimSpace(stringValue(chunk["toolName"])))
		part["state"] = "output-error"
		part["input"] = jsonutil.DeepCloneAny(chunk["input"])
		part["errorText"] = stringValue(chunk["errorText"])
		if title := strings.TrimSpace(stringValue(chunk["title"])); title != "" {
			part["title"] = title
		}
		if providerExecuted, ok := boolValue(chunk["providerExecuted"]); ok {
			part["providerExecuted"] = providerExecuted
		}
		if providerMetadata := jsonutil.DeepCloneMap(jsonutil.ToMap(chunk["providerMetadata"])); len(providerMetadata) > 0 {
			part["callProviderMetadata"] = providerMetadata
		}
	case "tool-approval-request":
		toolCallID := strings.TrimSpace(stringValue(chunk["toolCallId"]))
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, strings.TrimSpace(stringValue(state.UIToolNameByToolCallID[toolCallID])))
		part["state"] = "approval-requested"
		part["approval"] = map[string]any{"id": strings.TrimSpace(stringValue(chunk["approvalId"]))}
	case "tool-output-available":
		toolCallID := strings.TrimSpace(stringValue(chunk["toolCallId"]))
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, strings.TrimSpace(stringValue(state.UIToolNameByToolCallID[toolCallID])))
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
		toolCallID := strings.TrimSpace(stringValue(chunk["toolCallId"]))
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, strings.TrimSpace(stringValue(state.UIToolNameByToolCallID[toolCallID])))
		part["state"] = "output-error"
		part["errorText"] = stringValue(chunk["errorText"])
		if providerExecuted, ok := boolValue(chunk["providerExecuted"]); ok {
			part["providerExecuted"] = providerExecuted
		}
	case "tool-output-denied":
		toolCallID := strings.TrimSpace(stringValue(chunk["toolCallId"]))
		if toolCallID == "" {
			return
		}
		part := ensureToolPart(state, toolCallID, strings.TrimSpace(stringValue(state.UIToolNameByToolCallID[toolCallID])))
		part["state"] = "output-denied"
	case "source-url", "source-document", "file":
		appendPart(state, jsonutil.DeepCloneMap(jsonutil.ToMap(chunk)))
	case "finish":
		mergeMessageMetadata(ensureAssistantMessage(state), chunk["messageMetadata"])
	case "error":
		setTerminalState(ensureAssistantMessage(state), "error", stringValue(chunk["errorText"]))
	case "abort":
		setTerminalState(ensureAssistantMessage(state), "abort", strings.TrimSpace(stringValue(chunk["reason"])))
	default:
		if strings.HasPrefix(typ, "data-") {
			if transient, ok := boolValue(chunk["transient"]); ok && transient {
				return
			}
			appendOrReplaceDataPart(state, jsonutil.DeepCloneMap(jsonutil.ToMap(chunk)))
		}
	}
}

func SnapshotCanonicalUIMessage(state *UIState) map[string]any {
	if state == nil || len(state.UICanonicalMessage) == 0 {
		return nil
	}
	return jsonutil.DeepCloneMap(jsonutil.ToMap(state.UICanonicalMessage))
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
	if state.UICanonicalMessage == nil {
		state.UICanonicalMessage = map[string]any{
			"id":    state.TurnID,
			"role":  "assistant",
			"parts": []any{},
		}
	}
	if strings.TrimSpace(stringValue(state.UICanonicalMessage["id"])) == "" {
		state.UICanonicalMessage["id"] = state.TurnID
	}
	if strings.TrimSpace(stringValue(state.UICanonicalMessage["role"])) == "" {
		state.UICanonicalMessage["role"] = "assistant"
	}
	if _, ok := state.UICanonicalMessage["parts"].([]any); !ok {
		state.UICanonicalMessage["parts"] = []any{}
	}
	return state.UICanonicalMessage
}

func appendPart(state *UIState, part map[string]any) int {
	msg := ensureAssistantMessage(state)
	parts, _ := msg["parts"].([]any)
	idx := len(parts)
	msg["parts"] = append(parts, part)
	return idx
}

func ensureTextPart(state *UIState, partID string, providerMetadata map[string]any) map[string]any {
	if idx, ok := state.UITextPartIndexByID[partID]; ok {
		return getPartAt(state, idx)
	}
	part := newStreamingTextPart("text", providerMetadata)
	state.UITextPartIndexByID[partID] = appendPart(state, part)
	return part
}

func ensureReasoningPart(state *UIState, partID string, providerMetadata map[string]any) map[string]any {
	if idx, ok := state.UIReasoningPartIndexByID[partID]; ok {
		return getPartAt(state, idx)
	}
	part := newStreamingTextPart("reasoning", providerMetadata)
	state.UIReasoningPartIndexByID[partID] = appendPart(state, part)
	return part
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
	partType := strings.TrimSpace(stringValue(part["type"]))
	partID := strings.TrimSpace(stringValue(part["id"]))
	if partID != "" {
		for idx, raw := range parts {
			existing, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(stringValue(existing["type"])) == partType && strings.TrimSpace(stringValue(existing["id"])) == partID {
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
	if strings.TrimSpace(reason) != "" && typ == "error" {
		terminal["errorText"] = strings.TrimSpace(reason)
	}
	metadata["beeper_terminal_state"] = terminal
	message["metadata"] = metadata
}

func stringValue(raw any) string {
	if value, ok := raw.(string); ok {
		return value
	}
	return ""
}

func boolValue(raw any) (bool, bool) {
	value, ok := raw.(bool)
	return value, ok
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
