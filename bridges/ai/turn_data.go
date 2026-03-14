package ai

import (
	"strings"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/jsonutil"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/sdk"
)

func canonicalTurnData(meta *MessageMetadata) (sdk.TurnData, bool) {
	if meta == nil || meta.CanonicalTurnSchema != sdk.CanonicalTurnDataSchemaV1 || len(meta.CanonicalTurnData) == 0 {
		return sdk.TurnData{}, false
	}
	return sdk.DecodeTurnData(meta.CanonicalTurnData)
}

func promptMessagesFromTurnData(td sdk.TurnData) []PromptMessage {
	if td.Role == "" {
		return nil
	}
	switch td.Role {
	case "user":
		msg := PromptMessage{Role: PromptRoleUser}
		for _, part := range td.Parts {
			switch part.Type {
			case "text":
				if strings.TrimSpace(part.Text) != "" {
					msg.Blocks = append(msg.Blocks, PromptBlock{Type: PromptBlockText, Text: part.Text})
				}
			case "image":
				if strings.TrimSpace(part.URL) != "" {
					msg.Blocks = append(msg.Blocks, PromptBlock{Type: PromptBlockImage, ImageURL: part.URL, MimeType: part.MediaType})
				}
			case "file":
				if strings.TrimSpace(part.URL) != "" || strings.TrimSpace(part.Filename) != "" {
					msg.Blocks = append(msg.Blocks, PromptBlock{
						Type:     PromptBlockFile,
						FileURL:  part.URL,
						Filename: part.Filename,
						MimeType: part.MediaType,
					})
				}
			}
		}
		if len(msg.Blocks) == 0 {
			return nil
		}
		return []PromptMessage{msg}
	case "assistant":
		assistant := PromptMessage{Role: PromptRoleAssistant}
		var results []PromptMessage
		for _, part := range td.Parts {
			switch part.Type {
			case "text":
				if strings.TrimSpace(part.Text) != "" {
					assistant.Blocks = append(assistant.Blocks, PromptBlock{Type: PromptBlockText, Text: part.Text})
				}
			case "reasoning":
				text := strings.TrimSpace(part.Reasoning)
				if text == "" {
					text = strings.TrimSpace(part.Text)
				}
				if text != "" {
					assistant.Blocks = append(assistant.Blocks, PromptBlock{Type: PromptBlockThinking, Text: text})
				}
			case "tool":
				if strings.TrimSpace(part.ToolCallID) != "" && strings.TrimSpace(part.ToolName) != "" {
					assistant.Blocks = append(assistant.Blocks, PromptBlock{
						Type:              PromptBlockToolCall,
						ToolCallID:        part.ToolCallID,
						ToolName:          part.ToolName,
						ToolCallArguments: canonicalToolArguments(part.Input),
					})
				}
				outputText := strings.TrimSpace(formatCanonicalValue(part.Output))
				if outputText == "" {
					outputText = strings.TrimSpace(part.ErrorText)
				}
				if outputText == "" && part.State == "output-denied" {
					outputText = "Denied by user"
				}
				if strings.TrimSpace(part.ToolCallID) != "" && outputText != "" {
					results = append(results, PromptMessage{
						Role:       PromptRoleToolResult,
						ToolCallID: part.ToolCallID,
						ToolName:   part.ToolName,
						IsError:    strings.TrimSpace(part.ErrorText) != "",
						Blocks: []PromptBlock{{
							Type: PromptBlockText,
							Text: outputText,
						}},
					})
				}
			}
		}
		if len(assistant.Blocks) == 0 && len(results) == 0 {
			return nil
		}
		out := make([]PromptMessage, 0, 1+len(results))
		if len(assistant.Blocks) > 0 {
			out = append(out, assistant)
		}
		out = append(out, results...)
		return out
	default:
		return nil
	}
}

func turnDataFromUserPromptMessages(messages []PromptMessage) (sdk.TurnData, bool) {
	if len(messages) == 0 {
		return sdk.TurnData{}, false
	}
	msg := messages[0]
	if msg.Role != PromptRoleUser {
		return sdk.TurnData{}, false
	}
	td := sdk.TurnData{Role: "user"}
	td.Parts = make([]sdk.TurnPart, 0, len(msg.Blocks))
	for _, block := range msg.Blocks {
		switch block.Type {
		case PromptBlockText:
			if strings.TrimSpace(block.Text) != "" {
				td.Parts = append(td.Parts, sdk.TurnPart{Type: "text", Text: block.Text})
			}
		case PromptBlockImage:
			url := strings.TrimSpace(block.ImageURL)
			if url == "" && strings.TrimSpace(block.ImageB64) != "" {
				mimeType := block.MimeType
				if mimeType == "" {
					mimeType = "image/jpeg"
				}
				url = buildDataURL(mimeType, block.ImageB64)
			}
			if url != "" {
				td.Parts = append(td.Parts, sdk.TurnPart{Type: "image", URL: url, MediaType: block.MimeType})
			}
		case PromptBlockFile:
			if strings.TrimSpace(block.FileURL) != "" || strings.TrimSpace(block.Filename) != "" {
				td.Parts = append(td.Parts, sdk.TurnPart{
					Type:      "file",
					URL:       block.FileURL,
					Filename:  block.Filename,
					MediaType: block.MimeType,
				})
			}
		}
	}
	return td, len(td.Parts) > 0
}

func turnDataFromStreamingState(state *streamingState, uiMessage map[string]any) sdk.TurnData {
	td, _ := sdk.TurnDataFromUIMessage(uiMessage)
	if td.ID == "" {
		td.ID = state.turnID
	}
	if td.Role == "" {
		td.Role = "assistant"
	}
	if td.Metadata == nil {
		td.Metadata = map[string]any{}
	}
	for k, v := range map[string]any{
		"turn_id":             state.turnID,
		"finish_reason":       state.finishReason,
		"prompt_tokens":       state.promptTokens,
		"completion_tokens":   state.completionTokens,
		"reasoning_tokens":    state.reasoningTokens,
		"response_id":         state.responseID,
		"started_at_ms":       state.startedAtMs,
		"completed_at_ms":     state.completedAtMs,
		"first_token_at_ms":   state.firstTokenAtMs,
		"network_message_id":  state.networkMessageID,
		"initial_event_id":    state.initialEventID,
		"source_event_id":     state.sourceEventID,
		"generated_file_refs": agentremote.GeneratedFileRefsFromParts(state.generatedFiles),
	} {
		td.Metadata[k] = v
	}
	if !turnDataHasPartType(td, "text") {
		if text := strings.TrimSpace(state.accumulated.String()); text != "" {
			td.Parts = append(td.Parts, sdk.TurnPart{Type: "text", State: "done", Text: text})
		}
	}
	if !turnDataHasPartType(td, "reasoning") {
		if reasoning := strings.TrimSpace(state.reasoning.String()); reasoning != "" {
			td.Parts = append(td.Parts, sdk.TurnPart{Type: "reasoning", State: "done", Reasoning: reasoning, Text: reasoning})
		}
	}
	for _, toolCall := range state.toolCalls {
		if turnDataHasToolCall(td, strings.TrimSpace(toolCall.CallID)) {
			continue
		}
		part := sdk.TurnPart{
			Type:       "tool",
			ToolCallID: strings.TrimSpace(toolCall.CallID),
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
	return td
}

func buildCanonicalTurnData(
	state *streamingState,
	meta *PortalMetadata,
	linkPreviews []map[string]any,
) sdk.TurnData {
	if state == nil {
		return sdk.TurnData{}
	}
	uiMessage := streamui.SnapshotCanonicalUIMessage(&state.ui)
	td := turnDataFromStreamingState(state, uiMessage)
	if len(td.Metadata) == 0 {
		td.Metadata = map[string]any{}
	}
	for k, v := range jsonutil.DeepCloneMap(buildTurnDataMetadata(state, meta)) {
		td.Metadata[k] = v
	}
	for _, rawPart := range buildSourceParts(state.sourceCitations, state.sourceDocuments, nil) {
		appendTurnDataArtifactPart(&td, rawPart)
	}
	for _, preview := range linkPreviews {
		appendTurnDataArtifactPart(&td, preview)
	}
	for _, file := range state.generatedFiles {
		if strings.TrimSpace(file.URL) == "" || turnDataHasURLPart(td, "file", file.URL) {
			continue
		}
		td.Parts = append(td.Parts, sdk.TurnPart{Type: "file", URL: file.URL, MediaType: file.MediaType})
	}
	return td
}

func appendTurnDataArtifactPart(td *sdk.TurnData, raw map[string]any) {
	if td == nil || len(raw) == 0 {
		return
	}
	partType := strings.TrimSpace(stringValue(raw["type"]))
	switch partType {
	case "source-url":
		url := strings.TrimSpace(stringValue(raw["url"]))
		if url == "" || turnDataHasURLPart(*td, partType, url) {
			return
		}
		td.Parts = append(td.Parts, sdk.TurnPart{
			Type:             partType,
			URL:              url,
			Title:            strings.TrimSpace(stringValue(raw["title"])),
			ProviderMetadata: jsonutil.DeepCloneMap(jsonutil.ToMap(raw["providerMetadata"])),
		})
	case "source-document":
		filename := strings.TrimSpace(stringValue(raw["filename"]))
		title := strings.TrimSpace(stringValue(raw["title"]))
		if turnDataHasFilePart(*td, partType, filename, title) {
			return
		}
		td.Parts = append(td.Parts, sdk.TurnPart{
			Type:      partType,
			Title:     title,
			Filename:  filename,
			MediaType: strings.TrimSpace(stringValue(raw["mediaType"])),
		})
	}
}

func buildTurnDataMetadata(state *streamingState, meta *PortalMetadata) map[string]any {
	if state == nil {
		return nil
	}
	modelID := ""
	if meta != nil && meta.ResolvedTarget != nil {
		modelID = strings.TrimSpace(meta.ResolvedTarget.ModelID)
	}
	return map[string]any{
		"turn_id":           state.turnID,
		"agent_id":          state.agentID,
		"model":             modelID,
		"finish_reason":     state.finishReason,
		"prompt_tokens":     state.promptTokens,
		"completion_tokens": state.completionTokens,
		"reasoning_tokens":  state.reasoningTokens,
		"total_tokens":      state.totalTokens,
		"started_at_ms":     state.startedAtMs,
		"first_token_at_ms": state.firstTokenAtMs,
		"completed_at_ms":   state.completedAtMs,
	}
}

func turnDataHasPartType(td sdk.TurnData, partType string) bool {
	for _, part := range td.Parts {
		if part.Type == partType {
			return true
		}
	}
	return false
}

func turnDataHasToolCall(td sdk.TurnData, callID string) bool {
	for _, part := range td.Parts {
		if part.Type == "tool" && strings.TrimSpace(part.ToolCallID) == callID {
			return true
		}
	}
	return false
}

func turnDataHasURLPart(td sdk.TurnData, partType, url string) bool {
	for _, part := range td.Parts {
		if part.Type == partType && strings.TrimSpace(part.URL) == url {
			return true
		}
	}
	return false
}

func turnDataHasFilePart(td sdk.TurnData, partType, filename, title string) bool {
	for _, part := range td.Parts {
		if part.Type == partType && strings.TrimSpace(part.Filename) == strings.TrimSpace(filename) && strings.TrimSpace(part.Title) == strings.TrimSpace(title) {
			return true
		}
	}
	return false
}
