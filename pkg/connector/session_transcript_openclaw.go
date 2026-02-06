package connector

import (
	"encoding/json"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2/database"
)

const (
	openClawDefaultHistoryLimit = 200
	openClawHardHistoryLimit    = 1000
	openClawMaxHistoryBytes     = 6 * 1024 * 1024
)

type openClawToolCall struct {
	ID            string
	Name          string
	Input         map[string]any
	Output        map[string]any
	ResultStatus  string
	ErrorMessage  string
	CallEventID   string
	ResultEventID string
}

func normalizeOpenClawHistoryLimit(raw int) int {
	limit := openClawDefaultHistoryLimit
	if raw > 0 {
		limit = raw
	}
	if limit > openClawHardHistoryLimit {
		limit = openClawHardHistoryLimit
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

func stripOpenClawToolResults(messages []map[string]any) []map[string]any {
	filtered := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		if strings.TrimSpace(toString(msg["role"])) == "toolResult" {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

func capOpenClawHistoryByJSONBytes(items []map[string]any, maxBytes int) []map[string]any {
	if len(items) == 0 || maxBytes <= 0 {
		return items
	}
	parts := make([]int, len(items))
	total := 2 // []
	for i, item := range items {
		b, err := json.Marshal(item)
		if err != nil {
			parts[i] = len(fmt.Sprint(item))
		} else {
			parts[i] = len(b)
		}
		total += parts[i]
		if i > 0 {
			total += 1 // comma
		}
	}
	start := 0
	for total > maxBytes && start < len(items)-1 {
		total -= parts[start]
		if start < len(items)-1 {
			total -= 1
		}
		start++
	}
	if start > 0 {
		return items[start:]
	}
	return items
}

func buildOpenClawSessionMessages(messages []*database.Message, includeTools bool) []map[string]any {
	projected := projectOpenClawMessages(messages)
	repaired := repairOpenClawToolPairing(projected)
	if !includeTools {
		repaired = stripOpenClawToolResults(repaired)
	}
	return repaired
}

func projectOpenClawMessages(messages []*database.Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages)*2)
	for _, msg := range messages {
		meta := messageMeta(msg)
		if meta == nil {
			continue
		}
		role := strings.TrimSpace(meta.Role)
		switch role {
		case "user":
			entry := map[string]any{
				"role":      "user",
				"content":   buildTextBlocks(meta.Body),
				"timestamp": msg.Timestamp.UnixMilli(),
			}
			if msg.MXID != "" {
				entry["id"] = msg.MXID.String()
			}
			out = append(out, entry)
		case "assistant":
			assistant, calls := projectAssistantOpenClawMessage(meta, msg)
			out = append(out, assistant)
			for idx, call := range calls {
				toolResult := projectToolResultOpenClawMessage(call, msg, idx)
				out = append(out, toolResult)
			}
		case "toolResult":
			// Legacy persisted tool rows (rare): normalize them to OpenClaw shape.
			entry := map[string]any{
				"role":      "toolResult",
				"content":   buildTextBlocks(meta.Body),
				"timestamp": msg.Timestamp.UnixMilli(),
			}
			if msg.MXID != "" {
				entry["id"] = msg.MXID.String()
			}
			out = append(out, entry)
		}
	}
	return out
}

func projectAssistantOpenClawMessage(meta *MessageMetadata, msg *database.Message) (map[string]any, []openClawToolCall) {
	content := make([]map[string]any, 0, 1+len(meta.ToolCalls))
	calls := make([]openClawToolCall, 0, len(meta.ToolCalls))

	if canonicalBlocks, canonicalCalls := parseCanonicalAssistantBlocks(meta); len(canonicalBlocks) > 0 || len(canonicalCalls) > 0 {
		content = append(content, canonicalBlocks...)
		calls = append(calls, canonicalCalls...)
	}

	if len(calls) == 0 && len(meta.ToolCalls) > 0 {
		for idx, tc := range meta.ToolCalls {
			callID := strings.TrimSpace(tc.CallID)
			if callID == "" {
				callID = fmt.Sprintf("call_%s_%d", msg.MXID.String(), idx)
			}
			toolName := strings.TrimSpace(tc.ToolName)
			if toolName == "" {
				toolName = "unknown_tool"
			}
			arguments := tc.Input
			if arguments == nil {
				arguments = map[string]any{}
			}
			content = append(content, map[string]any{
				"type":      "toolCall",
				"id":        callID,
				"name":      toolName,
				"arguments": arguments,
			})
			calls = append(calls, openClawToolCall{
				ID:            callID,
				Name:          toolName,
				Input:         arguments,
				Output:        tc.Output,
				ResultStatus:  tc.ResultStatus,
				ErrorMessage:  tc.ErrorMessage,
				CallEventID:   tc.CallEventID,
				ResultEventID: tc.ResultEventID,
			})
		}
	}

	if len(content) == 0 {
		content = append(content, buildTextBlocks(meta.Body)...)
	}
	if len(content) == 0 {
		content = append(content, map[string]any{
			"type": "text",
			"text": "",
		})
	}

	entry := map[string]any{
		"role":      "assistant",
		"content":   content,
		"timestamp": msg.Timestamp.UnixMilli(),
	}
	if msg.MXID != "" {
		entry["id"] = msg.MXID.String()
	}
	return entry, calls
}

func parseCanonicalAssistantBlocks(meta *MessageMetadata) ([]map[string]any, []openClawToolCall) {
	partsRaw, ok := meta.CanonicalUIMessage["parts"]
	if !ok {
		return nil, nil
	}
	parts, ok := partsRaw.([]any)
	if !ok {
		return nil, nil
	}
	content := make([]map[string]any, 0, len(parts))
	calls := make([]openClawToolCall, 0, len(parts))
	toolCallByID := make(map[string]ToolCallMetadata, len(meta.ToolCalls))
	for _, tc := range meta.ToolCalls {
		callID := strings.TrimSpace(tc.CallID)
		if callID != "" {
			toolCallByID[callID] = tc
		}
	}

	for idx, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		partType := strings.TrimSpace(toString(part["type"]))
		switch partType {
		case "text":
			text := toString(part["text"])
			content = append(content, map[string]any{
				"type": "text",
				"text": text,
			})
		case "dynamic-tool":
			callID := strings.TrimSpace(toString(part["toolCallId"]))
			if callID == "" {
				callID = fmt.Sprintf("call_part_%d", idx)
			}
			toolName := strings.TrimSpace(toString(part["toolName"]))
			if toolName == "" {
				toolName = "unknown_tool"
			}
			args := toMapAny(part["input"])
			if args == nil {
				args = map[string]any{}
			}
			content = append(content, map[string]any{
				"type":      "toolCall",
				"id":        callID,
				"name":      toolName,
				"arguments": args,
			})
			call := openClawToolCall{
				ID:    callID,
				Name:  toolName,
				Input: args,
			}
			if tc, found := toolCallByID[callID]; found {
				call.Output = tc.Output
				call.ResultStatus = tc.ResultStatus
				call.ErrorMessage = tc.ErrorMessage
				call.CallEventID = tc.CallEventID
				call.ResultEventID = tc.ResultEventID
				if call.Name == "unknown_tool" && strings.TrimSpace(tc.ToolName) != "" {
					call.Name = tc.ToolName
				}
				if len(call.Input) == 0 && tc.Input != nil {
					call.Input = tc.Input
				}
			} else {
				call.Output = toMapAny(part["output"])
				state := strings.TrimSpace(toString(part["state"]))
				if strings.HasPrefix(state, "output-error") {
					call.ResultStatus = string(ResultStatusError)
					call.ErrorMessage = strings.TrimSpace(toString(part["errorText"]))
				} else if strings.HasPrefix(state, "output-") {
					call.ResultStatus = string(ResultStatusSuccess)
				}
			}
			calls = append(calls, call)
		}
	}

	return content, calls
}

func projectToolResultOpenClawMessage(call openClawToolCall, msg *database.Message, index int) map[string]any {
	callID := strings.TrimSpace(call.ID)
	if callID == "" {
		callID = fmt.Sprintf("call_%s_%d", msg.MXID.String(), index)
	}
	toolName := strings.TrimSpace(call.Name)
	if toolName == "" {
		toolName = "unknown_tool"
	}
	resultText := renderOpenClawToolResultText(call)
	isError := isOpenClawToolResultError(call)
	toolResult := map[string]any{
		"role":       "toolResult",
		"toolCallId": callID,
		"toolName":   toolName,
		"isError":    isError,
		"content": []map[string]any{
			{
				"type": "text",
				"text": resultText,
			},
		},
		"timestamp": msg.Timestamp.UnixMilli(),
	}
	if call.ResultEventID != "" {
		toolResult["id"] = call.ResultEventID
	}
	if len(call.Output) > 0 {
		toolResult["details"] = call.Output
	}
	return toolResult
}

func renderOpenClawToolResultText(call openClawToolCall) string {
	if call.Output != nil {
		if text, ok := call.Output["result"].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
		if payload, err := json.Marshal(call.Output); err == nil {
			return string(payload)
		}
	}
	if strings.TrimSpace(call.ErrorMessage) != "" {
		return call.ErrorMessage
	}
	return ""
}

func isOpenClawToolResultError(call openClawToolCall) bool {
	status := strings.ToLower(strings.TrimSpace(call.ResultStatus))
	if status == string(ResultStatusError) || status == "failed" || status == "timeout" || status == "cancelled" {
		return true
	}
	if strings.TrimSpace(call.ErrorMessage) != "" {
		return true
	}
	return false
}

func repairOpenClawToolPairing(messages []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	seenToolResults := make(map[string]struct{})

	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		role := strings.TrimSpace(toString(msg["role"]))
		if role != "assistant" {
			// Tool results must be adjacent to their assistant tool call turn.
			if role != "toolResult" {
				out = append(out, msg)
			}
			continue
		}

		toolCalls := extractOpenClawToolCalls(msg)
		if len(toolCalls) == 0 {
			out = append(out, msg)
			continue
		}

		toolCallSet := make(map[string]struct{}, len(toolCalls))
		for _, call := range toolCalls {
			toolCallSet[call.ID] = struct{}{}
		}

		spanResults := make(map[string]map[string]any, len(toolCalls))
		remainder := make([]map[string]any, 0, 4)

		j := i + 1
		for ; j < len(messages); j++ {
			next := messages[j]
			nextRole := strings.TrimSpace(toString(next["role"]))
			if nextRole == "assistant" {
				break
			}
			if nextRole == "toolResult" {
				id := extractOpenClawToolResultID(next)
				if id != "" {
					if _, ok := toolCallSet[id]; ok {
						if _, dup := seenToolResults[id]; dup {
							continue
						}
						if _, exists := spanResults[id]; !exists {
							spanResults[id] = next
						}
						continue
					}
				}
				// orphan or unrelated tool result: drop.
				continue
			}
			remainder = append(remainder, next)
		}

		out = append(out, msg)
		for _, call := range toolCalls {
			if existing, ok := spanResults[call.ID]; ok {
				seenToolResults[call.ID] = struct{}{}
				out = append(out, existing)
				continue
			}
			synthetic := map[string]any{
				"role":       "toolResult",
				"toolCallId": call.ID,
				"toolName":   call.Name,
				"isError":    true,
				"content": []map[string]any{
					{
						"type": "text",
						"text": "[openclaw] missing tool result in session history; inserted synthetic error result for transcript repair.",
					},
				},
			}
			seenToolResults[call.ID] = struct{}{}
			out = append(out, synthetic)
		}
		out = append(out, remainder...)
		i = j - 1
	}

	return out
}

type openClawToolCallPair struct {
	ID   string
	Name string
}

func extractOpenClawToolCalls(msg map[string]any) []openClawToolCallPair {
	contentRaw, ok := msg["content"]
	if !ok {
		return nil
	}
	blocks, ok := contentRaw.([]map[string]any)
	if !ok {
		list, ok := contentRaw.([]any)
		if !ok {
			return nil
		}
		blocks = make([]map[string]any, 0, len(list))
		for _, item := range list {
			if rec, ok := item.(map[string]any); ok {
				blocks = append(blocks, rec)
			}
		}
	}
	out := make([]openClawToolCallPair, 0, len(blocks))
	for _, block := range blocks {
		blockType := strings.TrimSpace(toString(block["type"]))
		if blockType != "toolCall" && blockType != "toolUse" && blockType != "functionCall" {
			continue
		}
		id := strings.TrimSpace(toString(block["id"]))
		if id == "" {
			continue
		}
		out = append(out, openClawToolCallPair{
			ID:   id,
			Name: strings.TrimSpace(toString(block["name"])),
		})
	}
	return out
}

func extractOpenClawToolResultID(msg map[string]any) string {
	if id := strings.TrimSpace(toString(msg["toolCallId"])); id != "" {
		return id
	}
	return strings.TrimSpace(toString(msg["toolUseId"]))
}

func buildTextBlocks(text string) []map[string]any {
	return []map[string]any{
		{
			"type": "text",
			"text": text,
		},
	}
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return str
	}
	return fmt.Sprint(value)
}

func toMapAny(value any) map[string]any {
	switch v := value.(type) {
	case map[string]any:
		return v
	case nil:
		return nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		out := map[string]any{}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil
		}
		return out
	}
}
