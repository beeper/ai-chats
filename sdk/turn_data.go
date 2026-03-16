package sdk

import (
	"encoding/json"

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

// TurnData is the SDK-owned semantic turn record used as the canonical source
// of truth for persistence. PromptContext and UIMessage are derived views.
type TurnData struct {
	ID       string         `json:"id,omitempty"`
	Role     string         `json:"role,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Extra    map[string]any `json:"extra,omitempty"`
	Parts    []TurnPart     `json:"parts,omitempty"`
}

// TurnPart is a semantic unit within a turn. It intentionally keeps a stable
// shape that can be projected into UI parts and provider prompt messages.
type TurnPart struct {
	Type             string         `json:"type"`
	State            string         `json:"state,omitempty"`
	Text             string         `json:"text,omitempty"`
	Reasoning        string         `json:"reasoning,omitempty"`
	ToolCallID       string         `json:"toolCallId,omitempty"`
	ToolName         string         `json:"toolName,omitempty"`
	ToolType         string         `json:"toolType,omitempty"`
	Input            any            `json:"input,omitempty"`
	Output           any            `json:"output,omitempty"`
	ErrorText        string         `json:"errorText,omitempty"`
	Approval         map[string]any `json:"approval,omitempty"`
	URL              string         `json:"url,omitempty"`
	Title            string         `json:"title,omitempty"`
	Filename         string         `json:"filename,omitempty"`
	MediaType        string         `json:"mediaType,omitempty"`
	ProviderExecuted bool           `json:"providerExecuted,omitempty"`
	Extra            map[string]any `json:"extra,omitempty"`
}

func (td TurnData) Clone() TurnData {
	data, err := json.Marshal(td)
	if err != nil {
		return TurnData{
			ID:       td.ID,
			Role:     td.Role,
			Metadata: jsonutil.DeepCloneMap(td.Metadata),
			Extra:    jsonutil.DeepCloneMap(td.Extra),
			Parts:    append([]TurnPart(nil), td.Parts...),
		}
	}
	var cloned TurnData
	if err = json.Unmarshal(data, &cloned); err != nil {
		return TurnData{
			ID:       td.ID,
			Role:     td.Role,
			Metadata: jsonutil.DeepCloneMap(td.Metadata),
			Extra:    jsonutil.DeepCloneMap(td.Extra),
			Parts:    append([]TurnPart(nil), td.Parts...),
		}
	}
	return cloned
}

func (td TurnData) ToMap() map[string]any {
	data, err := json.Marshal(td)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err = json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func DecodeTurnData(raw map[string]any) (TurnData, bool) {
	if len(raw) == 0 {
		return TurnData{}, false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return TurnData{}, false
	}
	var td TurnData
	if err = json.Unmarshal(data, &td); err != nil {
		return TurnData{}, false
	}
	return td, true
}

// TurnDataFromUIMessage derives semantic turn data from a UIMessage. This is
// primarily used by the SDK turn runtime, where the canonical turn record is
// assembled from the same streaming state that drives UI deltas.
func TurnDataFromUIMessage(uiMessage map[string]any) (TurnData, bool) {
	if len(uiMessage) == 0 {
		return TurnData{}, false
	}
	td := TurnData{
		ID:       stringValue(uiMessage["id"]),
		Role:     stringValue(uiMessage["role"]),
		Metadata: jsonutil.DeepCloneMap(jsonutil.ToMap(uiMessage["metadata"])),
		Extra:    extraFields(uiMessage, "id", "role", "metadata", "parts"),
	}
	var partsRaw []any
	switch typed := uiMessage["parts"].(type) {
	case []any:
		partsRaw = typed
	case []map[string]any:
		partsRaw = make([]any, 0, len(typed))
		for _, part := range typed {
			partsRaw = append(partsRaw, part)
		}
	default:
		return td, td.Role != "" || td.ID != ""
	}
	td.Parts = make([]TurnPart, 0, len(partsRaw))
	for _, rawPart := range partsRaw {
		partMap, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		part := TurnPart{
			Type:       normalizeTurnPartType(stringValue(partMap["type"])),
			State:      stringValue(partMap["state"]),
			Text:       stringValue(partMap["text"]),
			Reasoning:  stringValue(partMap["reasoning"]),
			ToolCallID: stringValue(partMap["toolCallId"]),
			ToolName:   stringValue(partMap["toolName"]),
			ToolType:   stringValue(partMap["toolType"]),
			Input:      jsonutil.DeepCloneAny(partMap["input"]),
			Output:     jsonutil.DeepCloneAny(partMap["output"]),
			ErrorText:  stringValue(partMap["errorText"]),
			Approval:   jsonutil.DeepCloneMap(jsonutil.ToMap(partMap["approval"])),
			URL:        stringValue(partMap["url"]),
			Title:      stringValue(partMap["title"]),
			Filename:   stringValue(partMap["filename"]),
			MediaType:  stringValue(partMap["mediaType"]),
			Extra:      extraFields(partMap, "type", "state", "text", "reasoning", "toolCallId", "toolName", "toolType", "input", "output", "errorText", "approval", "url", "title", "filename", "mediaType", "providerExecuted"),
		}
		if value, ok := partMap["providerExecuted"].(bool); ok {
			part.ProviderExecuted = value
		}
		td.Parts = append(td.Parts, part)
	}
	return td, td.Role != "" || td.ID != "" || len(td.Parts) > 0
}

func normalizeTurnPartType(partType string) string {
	switch partType {
	case "dynamic-tool":
		return "tool"
	default:
		return partType
	}
}

// UIMessageFromTurnData projects canonical turn data into an AI SDK UIMessage
// shape suitable for Matrix transport.
func UIMessageFromTurnData(td TurnData) map[string]any {
	ui := map[string]any{
		"id":   td.ID,
		"role": td.Role,
	}
	if len(td.Metadata) > 0 {
		ui["metadata"] = jsonutil.DeepCloneMap(td.Metadata)
	}
	for key, value := range jsonutil.DeepCloneMap(td.Extra) {
		ui[key] = value
	}
	parts := make([]any, 0, len(td.Parts))
	for _, part := range td.Parts {
		partMap := map[string]any{
			"type": part.Type,
		}
		if part.State != "" {
			partMap["state"] = part.State
		}
		if part.Text != "" {
			partMap["text"] = part.Text
		}
		if part.Reasoning != "" {
			partMap["reasoning"] = part.Reasoning
		}
		if part.ToolCallID != "" {
			partMap["toolCallId"] = part.ToolCallID
		}
		if part.ToolName != "" {
			partMap["toolName"] = part.ToolName
		}
		if part.ToolType != "" {
			partMap["toolType"] = part.ToolType
		}
		if part.Input != nil {
			partMap["input"] = jsonutil.DeepCloneAny(part.Input)
		}
		if part.Output != nil {
			partMap["output"] = jsonutil.DeepCloneAny(part.Output)
		}
		if part.ErrorText != "" {
			partMap["errorText"] = part.ErrorText
		}
		if len(part.Approval) > 0 {
			partMap["approval"] = jsonutil.DeepCloneMap(part.Approval)
		}
		if part.URL != "" {
			partMap["url"] = part.URL
		}
		if part.Title != "" {
			partMap["title"] = part.Title
		}
		if part.Filename != "" {
			partMap["filename"] = part.Filename
		}
		if part.MediaType != "" {
			partMap["mediaType"] = part.MediaType
		}
		if part.ProviderExecuted {
			partMap["providerExecuted"] = true
		}
		for key, value := range jsonutil.DeepCloneMap(part.Extra) {
			partMap[key] = value
		}
		parts = append(parts, partMap)
	}
	ui["parts"] = parts
	return ui
}

func extraFields(raw map[string]any, knownKeys ...string) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	known := make(map[string]struct{}, len(knownKeys))
	for _, key := range knownKeys {
		known[key] = struct{}{}
	}
	extra := map[string]any{}
	for key, value := range raw {
		if _, ok := known[key]; ok {
			continue
		}
		extra[key] = jsonutil.DeepCloneAny(value)
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}
