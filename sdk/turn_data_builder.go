package sdk

import (
	"strings"

	"github.com/beeper/ai-chats/pkg/shared/jsonutil"
)

// TurnDataBuildOptions describes provider/runtime-specific data that should be
// merged into canonical turn data derived from a UI message snapshot.
type TurnDataBuildOptions struct {
	ID             string
	Role           string
	Metadata       map[string]any
	Text           string
	Reasoning      string
	ToolCalls      []ToolCallMetadata
	GeneratedFiles []GeneratedFileRef
	ArtifactParts  []map[string]any
}

// BuildTurnDataFromUIMessage merges semantic runtime data into turn data
// derived from a UIMessage snapshot.
func BuildTurnDataFromUIMessage(uiMessage map[string]any, opts TurnDataBuildOptions) TurnData {
	td, _ := TurnDataFromUIMessage(uiMessage)
	if td.ID == "" {
		td.ID = strings.TrimSpace(opts.ID)
	}
	if td.Role == "" {
		td.Role = strings.TrimSpace(opts.Role)
	}
	if td.Metadata == nil {
		td.Metadata = map[string]any{}
	}
	for k, v := range jsonutil.DeepCloneMap(opts.Metadata) {
		td.Metadata[k] = v
	}
	if !TurnDataHasPartType(td, "text") {
		if text := strings.TrimSpace(opts.Text); text != "" {
			td.Parts = append(td.Parts, TurnPart{Type: "text", State: "done", Text: text})
		}
	}
	if !TurnDataHasPartType(td, "reasoning") {
		if reasoning := strings.TrimSpace(opts.Reasoning); reasoning != "" {
			td.Parts = append(td.Parts, TurnPart{Type: "reasoning", State: "done", Reasoning: reasoning, Text: reasoning})
		}
	}
	for _, toolCall := range opts.ToolCalls {
		callID := strings.TrimSpace(toolCall.CallID)
		if TurnDataHasToolCall(td, callID) {
			continue
		}
		part := TurnPart{
			Type:       "tool",
			ToolCallID: callID,
			ToolName:   strings.TrimSpace(toolCall.ToolName),
			ToolType:   strings.TrimSpace(toolCall.ToolType),
			State:      strings.TrimSpace(toolCall.Status),
			Input:      jsonutil.DeepCloneAny(toolCall.Input),
			Output:     jsonutil.DeepCloneAny(toolCall.Output),
			ErrorText:  strings.TrimSpace(toolCall.ErrorMessage),
		}
		if part.State == "" {
			part.State = "output-available"
		}
		td.Parts = append(td.Parts, part)
	}
	for _, raw := range opts.ArtifactParts {
		AppendArtifactPart(&td, raw)
	}
	for _, file := range opts.GeneratedFiles {
		if strings.TrimSpace(file.URL) == "" || TurnDataHasURLPart(td, "file", file.URL) {
			continue
		}
		td.Parts = append(td.Parts, TurnPart{
			Type:      "file",
			URL:       file.URL,
			MediaType: file.MimeType,
		})
	}
	return td
}

func AppendArtifactPart(td *TurnData, raw map[string]any) {
	if td == nil || len(raw) == 0 {
		return
	}
	partType := strings.TrimSpace(stringValue(raw["type"]))
	switch partType {
	case "source-url":
		url := strings.TrimSpace(stringValue(raw["url"]))
		if url == "" || TurnDataHasURLPart(*td, partType, url) {
			return
		}
		td.Parts = append(td.Parts, TurnPart{
			Type:  partType,
			URL:   url,
			Title: strings.TrimSpace(stringValue(raw["title"])),
			Extra: extraFields(raw, "type", "url", "title"),
		})
	case "source-document":
		filename := strings.TrimSpace(stringValue(raw["filename"]))
		title := strings.TrimSpace(stringValue(raw["title"]))
		if TurnDataHasFilePart(*td, partType, filename, title) {
			return
		}
		td.Parts = append(td.Parts, TurnPart{
			Type:      partType,
			Title:     title,
			Filename:  filename,
			MediaType: strings.TrimSpace(stringValue(raw["mediaType"])),
			Extra:     extraFields(raw, "type", "title", "filename", "mediaType"),
		})
	}
}

func TurnDataHasPartType(td TurnData, partType string) bool {
	for _, part := range td.Parts {
		if part.Type == partType {
			return true
		}
	}
	return false
}

func TurnDataHasToolCall(td TurnData, callID string) bool {
	for _, part := range td.Parts {
		if part.Type == "tool" && strings.TrimSpace(part.ToolCallID) == callID {
			return true
		}
	}
	return false
}

func TurnDataHasURLPart(td TurnData, partType, url string) bool {
	for _, part := range td.Parts {
		if part.Type == partType && strings.TrimSpace(part.URL) == url {
			return true
		}
	}
	return false
}

func TurnDataHasFilePart(td TurnData, partType, filename, title string) bool {
	filename = strings.TrimSpace(filename)
	title = strings.TrimSpace(title)
	for _, part := range td.Parts {
		if part.Type == partType && strings.TrimSpace(part.Filename) == filename && strings.TrimSpace(part.Title) == title {
			return true
		}
	}
	return false
}
