package connector

import (
	"encoding/json"
	"net/url"
	"path"
	"strings"

	"github.com/openai/openai-go/v3/responses"

	"github.com/beeper/ai-bridge/pkg/shared/citations"
)

func mergeMaps(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func toJSONObject(value any) map[string]any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil
	}
	return decoded
}

func parseJSONOrRaw(input string) any {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return map[string]any{}
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

func responseOutputItemToMap(item responses.ResponseOutputItemUnion) map[string]any {
	return toJSONObject(item)
}

type responseToolDescriptor struct {
	itemID           string
	callID           string
	toolName         string
	toolType         ToolType
	input            any
	providerExecuted bool
	dynamic          bool
	ok               bool
}

func deriveToolDescriptorForOutputItem(item responses.ResponseOutputItemUnion, state *streamingState) responseToolDescriptor {
	desc := responseToolDescriptor{
		itemID: item.ID,
		callID: item.ID,
	}
	switch item.Type {
	case "function_call":
		desc.callID = strings.TrimSpace(item.CallID)
		if desc.callID == "" {
			desc.callID = item.ID
		}
		desc.toolName = strings.TrimSpace(item.Name)
		desc.toolType = ToolTypeFunction
		desc.providerExecuted = false
		desc.dynamic = false
		desc.input = parseJSONOrRaw(item.Arguments)
		desc.ok = desc.toolName != ""
	case "web_search_call":
		desc.toolName = ToolNameWebSearch
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.input = map[string]any{}
		desc.ok = true
	case "file_search_call":
		desc.toolName = "file_search"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.input = map[string]any{}
		desc.ok = true
	case "image_generation_call":
		desc.toolName = "image_generation"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.input = map[string]any{}
		desc.ok = true
	case "code_interpreter_call":
		desc.toolName = "code_interpreter"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.input = map[string]any{
			"containerId": item.ContainerID,
			"code":        item.Code,
		}
		desc.ok = true
	case "computer_call":
		desc.toolName = "computer_use"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.input = map[string]any{}
		desc.ok = true
	case "local_shell_call":
		desc.callID = strings.TrimSpace(item.CallID)
		if desc.callID == "" {
			desc.callID = item.ID
		}
		desc.toolName = "local_shell"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.dynamic = true
		desc.input = responseOutputItemToMap(item)
		desc.ok = true
	case "shell_call":
		desc.callID = strings.TrimSpace(item.CallID)
		if desc.callID == "" {
			desc.callID = item.ID
		}
		desc.toolName = "shell"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.dynamic = true
		desc.input = responseOutputItemToMap(item)
		desc.ok = true
	case "apply_patch_call":
		desc.callID = strings.TrimSpace(item.CallID)
		if desc.callID == "" {
			desc.callID = item.ID
		}
		desc.toolName = "apply_patch"
		desc.toolType = ToolTypeProvider
		desc.providerExecuted = true
		desc.dynamic = true
		desc.input = responseOutputItemToMap(item)
		desc.ok = true
	case "custom_tool_call":
		desc.callID = strings.TrimSpace(item.CallID)
		if desc.callID == "" {
			desc.callID = item.ID
		}
		desc.toolName = strings.TrimSpace(item.Name)
		desc.toolType = ToolTypeFunction
		desc.providerExecuted = false
		desc.dynamic = true
		desc.input = parseJSONOrRaw(item.Input)
		desc.ok = desc.toolName != ""
	case "mcp_call":
		desc.toolName = "mcp." + strings.TrimSpace(item.Name)
		desc.toolType = ToolTypeMCP
		desc.providerExecuted = true
		desc.dynamic = true
		if approvalID := strings.TrimSpace(item.ApprovalRequestID); approvalID != "" && state != nil {
			if mapped := strings.TrimSpace(state.ui.UIToolCallIDByApproval[approvalID]); mapped != "" {
				desc.callID = mapped
			}
		}
		desc.input = parseJSONOrRaw(item.Arguments)
		desc.ok = strings.TrimSpace(item.Name) != ""
	case "mcp_list_tools":
		desc.toolName = "mcp.list_tools"
		desc.toolType = ToolTypeMCP
		desc.providerExecuted = true
		desc.dynamic = true
		desc.input = map[string]any{}
		desc.ok = true
	case "mcp_approval_request":
		desc.toolName = "mcp." + strings.TrimSpace(item.Name)
		desc.toolType = ToolTypeMCP
		desc.providerExecuted = true
		desc.dynamic = true
		desc.callID = NewCallID()
		desc.input = parseJSONOrRaw(item.Arguments)
		desc.ok = strings.TrimSpace(item.Name) != ""
	default:
		desc.ok = false
	}
	if strings.TrimSpace(desc.callID) == "" {
		desc.callID = NewCallID()
	}
	if desc.itemID == "" {
		desc.itemID = desc.callID
	}
	return desc
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
		if action := toJSONObject(item.Action); len(action) > 0 {
			result["action"] = action
		}
		return result
	case "file_search_call":
		return map[string]any{
			"queries": item.Queries,
			"results": item.Results,
			"status":  item.Status,
		}
	case "code_interpreter_call":
		return map[string]any{
			"outputs":     item.Outputs,
			"status":      item.Status,
			"containerId": item.ContainerID,
		}
	case "image_generation_call":
		return map[string]any{
			"status": item.Status,
			"result": item.Result,
		}
	case "mcp_call":
		result := map[string]any{
			"type":        "call",
			"serverLabel": item.ServerLabel,
			"name":        item.Name,
			"arguments":   item.Arguments,
			"status":      item.Status,
		}
		if output := strings.TrimSpace(item.Output.OfString); output != "" {
			result["output"] = parseJSONOrRaw(output)
		}
		if strings.TrimSpace(item.Error) != "" {
			result["error"] = item.Error
		}
		return result
	case "mcp_list_tools":
		result := map[string]any{
			"serverLabel": item.ServerLabel,
			"tools":       item.Tools,
		}
		if strings.TrimSpace(item.Error) != "" {
			result["error"] = item.Error
		}
		return result
	case "shell_call_output":
		if output := item.Output.OfResponseFunctionShellToolCallOutputOutputArray; len(output) > 0 {
			return map[string]any{"output": output}
		}
		if output := strings.TrimSpace(item.Output.OfString); output != "" {
			return parseJSONOrRaw(output)
		}
		return responseOutputItemToMap(item)
	default:
		if mapped := responseOutputItemToMap(item); len(mapped) > 0 {
			return mapped
		}
		return map[string]any{"status": item.Status}
	}
}

func codeInterpreterFileParts(item responses.ResponseOutputItemUnion) []citations.GeneratedFilePart {
	if item.Type != "code_interpreter_call" || len(item.Outputs) == 0 {
		return nil
	}
	files := make([]citations.GeneratedFilePart, 0, len(item.Outputs))
	for _, output := range item.Outputs {
		image := output.AsImage()
		if strings.TrimSpace(image.URL) == "" {
			continue
		}
		mediaType := codeInterpreterMediaTypeFromURL(image.URL)
		files = append(files, citations.GeneratedFilePart{
			URL:       strings.TrimSpace(image.URL),
			MediaType: mediaType,
		})
	}
	return files
}

func codeInterpreterMediaTypeFromURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	ext := ""
	if err == nil {
		ext = strings.ToLower(path.Ext(parsed.Path))
	}
	if ext == "" {
		ext = strings.ToLower(path.Ext(strings.TrimSpace(rawURL)))
	}
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	default:
		return "application/octet-stream"
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
