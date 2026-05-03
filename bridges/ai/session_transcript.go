package ai

import (
	"encoding/json"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2/database"

	runtimeparse "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

const (
	transcriptDefaultHistoryLimit = 200
	transcriptHardHistoryLimit    = 1000
	transcriptMaxHistoryBytes     = 6 * 1024 * 1024
)

type transcriptToolCall struct {
	ID            string
	Name          string
	Input         map[string]any
	Output        map[string]any
	ResultStatus  string
	ErrorMessage  string
	CallEventID   string
	ResultEventID string
}

func normalizeTranscriptHistoryLimit(raw int) int {
	limit := transcriptDefaultHistoryLimit
	if raw > 0 {
		limit = raw
	}
	if limit > transcriptHardHistoryLimit {
		limit = transcriptHardHistoryLimit
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

func stripTranscriptToolResults(messages []map[string]any) []map[string]any {
	filtered := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		if strings.TrimSpace(toString(msg["role"])) == "toolResult" {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

func capTranscriptHistoryByJSONBytes(items []map[string]any, maxBytes int) []map[string]any {
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

func buildTranscriptMessages(messages []*database.Message, includeTools bool) []map[string]any {
	projected := projectTranscriptMessages(messages)
	repaired := repairTranscriptToolPairing(projected)
	if !includeTools {
		repaired = stripTranscriptToolResults(repaired)
	}
	return repaired
}

func projectTranscriptMessages(messages []*database.Message) []map[string]any {
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
				"content":   buildTextBlocksForRole(meta.Body, true),
				"timestamp": msg.Timestamp.UnixMilli(),
			}
			if msg.MXID != "" {
				entry["id"] = msg.MXID.String()
			}
			out = append(out, entry)
		case "assistant":
			assistant, calls := projectAssistantTranscriptMessage(meta, msg)
			out = append(out, assistant)
			for idx, call := range calls {
				toolResult := projectToolResultTranscriptMessage(call, msg, idx)
				out = append(out, toolResult)
			}
		}
	}
	return out
}

func projectAssistantTranscriptMessage(meta *MessageMetadata, msg *database.Message) (map[string]any, []transcriptToolCall) {
	content := make([]map[string]any, 0, 1+len(meta.ToolCalls))
	calls := make([]transcriptToolCall, 0, len(meta.ToolCalls))

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
			calls = append(calls, transcriptToolCall{
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
		content = append(content, buildTextBlocksForRole(meta.Body, false)...)
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

func parseCanonicalAssistantBlocks(meta *MessageMetadata) ([]map[string]any, []transcriptToolCall) {
	if turnData, ok := canonicalTurnData(meta); ok {
		messages := promptMessagesFromTurnData(turnData)
		if len(messages) == 0 {
			return nil, nil
		}
		content := make([]map[string]any, 0, len(messages))
		calls := make([]transcriptToolCall, 0, len(messages))
		toolCallByID := make(map[string]ToolCallMetadata, len(meta.ToolCalls))
		for _, tc := range meta.ToolCalls {
			callID := strings.TrimSpace(tc.CallID)
			if callID != "" {
				toolCallByID[callID] = tc
			}
		}
		for _, message := range messages {
			if message.Role != PromptRoleAssistant {
				continue
			}
			for idx, block := range message.Blocks {
				switch block.Type {
				case PromptBlockText, PromptBlockThinking:
					if text := strings.TrimSpace(block.Text); text != "" {
						content = append(content, map[string]any{
							"type": "text",
							"text": text,
						})
					}
				case PromptBlockToolCall:
					callID := strings.TrimSpace(block.ToolCallID)
					if callID == "" {
						callID = fmt.Sprintf("call_part_%d", idx)
					}
					toolName := strings.TrimSpace(block.ToolName)
					if toolName == "" {
						toolName = "unknown_tool"
					}
					arguments := jsonutil.ToMap(block.ToolCallArguments)
					if arguments == nil {
						arguments = map[string]any{}
					}
					content = append(content, map[string]any{
						"type":      "toolCall",
						"id":        callID,
						"name":      toolName,
						"arguments": arguments,
					})
					call := transcriptToolCall{
						ID:    callID,
						Name:  toolName,
						Input: arguments,
					}
					if toolMeta, ok := toolCallByID[callID]; ok {
						call.Output = toolMeta.Output
						call.ResultStatus = toolMeta.ResultStatus
						call.ErrorMessage = toolMeta.ErrorMessage
						call.CallEventID = toolMeta.CallEventID
						call.ResultEventID = toolMeta.ResultEventID
					}
					calls = append(calls, call)
				}
			}
		}
		if len(content) > 0 || len(calls) > 0 {
			return content, calls
		}
	}

	return nil, nil
}

func projectToolResultTranscriptMessage(call transcriptToolCall, msg *database.Message, index int) map[string]any {
	callID := strings.TrimSpace(call.ID)
	if callID == "" {
		callID = fmt.Sprintf("call_%s_%d", msg.MXID.String(), index)
	}
	toolName := strings.TrimSpace(call.Name)
	if toolName == "" {
		toolName = "unknown_tool"
	}
	resultText := renderTranscriptToolResultText(call)
	isError := isTranscriptToolResultError(call)
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

func renderTranscriptToolResultText(call transcriptToolCall) string {
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

func isTranscriptToolResultError(call transcriptToolCall) bool {
	status := strings.ToLower(strings.TrimSpace(call.ResultStatus))
	if status == string(ResultStatusError) || status == string(ResultStatusDenied) || status == "failed" || status == "timeout" || status == "cancelled" {
		return true
	}
	if strings.TrimSpace(call.ErrorMessage) != "" {
		return true
	}
	return false
}

func repairTranscriptToolPairing(messages []map[string]any) []map[string]any {
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

		toolCalls := extractTranscriptToolCalls(msg)
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
				id := extractTranscriptToolResultID(next)
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
						"text": "[transcript] missing tool result in turn history; inserted synthetic error result for transcript repair.",
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

type transcriptToolCallPair struct {
	ID   string
	Name string
}

func extractTranscriptToolCalls(msg map[string]any) []transcriptToolCallPair {
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
	out := make([]transcriptToolCallPair, 0, len(blocks))
	for _, block := range blocks {
		blockType := strings.TrimSpace(toString(block["type"]))
		if blockType != "toolCall" && blockType != "toolUse" && blockType != "functionCall" {
			continue
		}
		id := strings.TrimSpace(toString(block["id"]))
		if id == "" {
			continue
		}
		out = append(out, transcriptToolCallPair{
			ID:   id,
			Name: strings.TrimSpace(toString(block["name"])),
		})
	}
	return out
}

func extractTranscriptToolResultID(msg map[string]any) string {
	if id := strings.TrimSpace(toString(msg["toolCallId"])); id != "" {
		return id
	}
	return strings.TrimSpace(toString(msg["toolUseId"]))
}

func buildTextBlocksForRole(text string, isUser bool) []map[string]any {
	cleaned := runtimeparse.SanitizeChatMessageForDisplay(text, isUser)
	return []map[string]any{
		{
			"type": "text",
			"text": cleaned,
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
