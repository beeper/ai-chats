package ai

import (
	"strings"

	"github.com/beeper/agentremote"
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
	return sdk.BuildTurnDataFromUIMessage(uiMessage, sdk.TurnDataBuildOptions{
		ID:   state.turnID,
		Role: "assistant",
		Metadata: map[string]any{
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
		},
		Text:      state.accumulated.String(),
		Reasoning: state.reasoning.String(),
		ToolCalls: state.toolCalls,
	})
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
	artifactParts := buildSourceParts(state.sourceCitations, state.sourceDocuments, nil)
	artifactParts = append(artifactParts, linkPreviews...)
	return sdk.BuildTurnDataFromUIMessage(sdk.UIMessageFromTurnData(td), sdk.TurnDataBuildOptions{
		ID:             td.ID,
		Role:           td.Role,
		Metadata:       buildTurnDataMetadata(state, meta),
		GeneratedFiles: agentremote.GeneratedFileRefsFromParts(state.generatedFiles),
		ArtifactParts:  artifactParts,
	})
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
