package ai

import (
	"encoding/json"
	"maps"
	"strings"

	"github.com/openai/openai-go/v3/responses"

	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

func mergeMaps(base map[string]any, extra map[string]any) map[string]any {
	out := maps.Clone(base)
	if out == nil {
		out = make(map[string]any, len(extra))
	}
	maps.Copy(out, extra)
	return out
}

func parseJSONOrRaw(input string) any {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return trimmed
	}
	return parsed
}

func stringifyJSONValue(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(encoded))
}

type responseToolDescriptor struct {
	registryKey      string
	itemID           string
	callID           string
	toolName         string
	toolType         matrixevents.ToolType
	input            any
	providerExecuted bool
	dynamic          bool
	ok               bool
}

func deriveToolDescriptorForOutputItem(item responses.ResponseOutputItemUnion, state *streamingState) responseToolDescriptor {
	desc := responseToolDescriptor{
		itemID:      item.ID,
		callID:      item.ID,
		registryKey: streamToolItemKey(item.ID),
	}
	switch item.Type {
	case "function_call":
		desc = responseFunctionToolDescriptor(item, false, parseJSONOrRaw(item.Arguments))
	case "image_generation_call":
		desc = responseProviderToolDescriptor(item, "image_generation", nil)
	default:
		desc.ok = false
	}
	if strings.TrimSpace(desc.callID) == "" {
		desc.callID = NewCallID()
	}
	if desc.itemID == "" {
		desc.itemID = desc.callID
	}
	if desc.registryKey == "" {
		desc.registryKey = streamToolItemKey(desc.itemID)
	}
	if desc.registryKey == "" {
		desc.registryKey = streamToolCallKey(desc.callID)
	}
	return desc
}

func responseProviderToolDescriptor(item responses.ResponseOutputItemUnion, toolName string, input any) responseToolDescriptor {
	callID := strings.TrimSpace(item.CallID)
	if callID == "" {
		callID = item.ID
	}
	return responseToolDescriptor{
		registryKey:      streamToolItemKey(item.ID),
		itemID:           item.ID,
		callID:           callID,
		toolName:         toolName,
		toolType:         matrixevents.ToolTypeProvider,
		input:            input,
		providerExecuted: true,
		ok:               toolName != "",
	}
}

func responseFunctionToolDescriptor(item responses.ResponseOutputItemUnion, dynamic bool, input any) responseToolDescriptor {
	callID := strings.TrimSpace(item.CallID)
	if callID == "" {
		callID = item.ID
	}
	toolName := strings.TrimSpace(item.Name)
	return responseToolDescriptor{
		registryKey:      streamToolItemKey(item.ID),
		itemID:           item.ID,
		callID:           callID,
		toolName:         toolName,
		toolType:         matrixevents.ToolTypeFunction,
		input:            input,
		providerExecuted: false,
		dynamic:          dynamic,
		ok:               toolName != "",
	}
}

func outputItemLooksDenied(item responses.ResponseOutputItemUnion) bool {
	errorText := strings.ToLower(strings.TrimSpace(item.Error))
	if strings.Contains(errorText, "denied") || strings.Contains(errorText, "rejected") {
		return true
	}
	status := strings.ToLower(strings.TrimSpace(item.Status))
	return status == "denied" || status == "rejected"
}

func responseOutputItemResultPayload(item responses.ResponseOutputItemUnion) any {
	switch item.Type {
	case "web_search_call":
		result := map[string]any{
			"status": item.Status,
		}
		if action := jsonutil.ToMap(item.Action); len(action) > 0 {
			result["action"] = action
		}
		return result
	default:
		if mapped := jsonutil.ToMap(item); len(mapped) > 0 {
			return mapped
		}
		return map[string]any{"status": item.Status}
	}
}

func responseMetadataDeltaFromResponse(resp responses.Response) map[string]any {
	metadata := map[string]any{}
	if strings.TrimSpace(resp.ID) != "" {
		metadata["response_id"] = resp.ID
	}
	if strings.TrimSpace(string(resp.Status)) != "" {
		metadata["response_status"] = string(resp.Status)
	}
	if strings.TrimSpace(string(resp.Model)) != "" {
		metadata["model"] = string(resp.Model)
	}
	return metadata
}
