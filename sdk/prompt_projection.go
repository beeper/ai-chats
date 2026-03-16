package sdk

import (
	"encoding/json"
	"fmt"
	"strings"
)

type PromptRole string

const (
	PromptRoleUser       PromptRole = "user"
	PromptRoleAssistant  PromptRole = "assistant"
	PromptRoleToolResult PromptRole = "tool_result"
)

type PromptBlockType string

const (
	PromptBlockText     PromptBlockType = "text"
	PromptBlockImage    PromptBlockType = "image"
	PromptBlockFile     PromptBlockType = "file"
	PromptBlockThinking PromptBlockType = "thinking"
	PromptBlockToolCall PromptBlockType = "tool_call"
	PromptBlockAudio    PromptBlockType = "audio"
	PromptBlockVideo    PromptBlockType = "video"
)

type PromptBlock struct {
	Type PromptBlockType

	Text string

	ImageURL string
	ImageB64 string
	MimeType string

	FileURL  string
	FileB64  string
	Filename string

	ToolCallID        string
	ToolName          string
	ToolCallArguments string

	AudioB64    string
	AudioFormat string

	VideoURL string
	VideoB64 string
}

type PromptMessage struct {
	Role       PromptRole
	Blocks     []PromptBlock
	ToolCallID string
	ToolName   string
	IsError    bool
}

func (m PromptMessage) Text() string {
	var texts []string
	for _, block := range m.Blocks {
		switch block.Type {
		case PromptBlockText, PromptBlockThinking:
			if strings.TrimSpace(block.Text) != "" {
				texts = append(texts, block.Text)
			}
		}
	}
	return strings.Join(texts, "\n")
}

func PromptMessagesFromTurnData(td TurnData) []PromptMessage {
	if td.Role == "" {
		return nil
	}
	switch td.Role {
	case "user":
		msg := PromptMessage{Role: PromptRoleUser}
		for _, part := range td.Parts {
			switch normalizeTurnPartType(part.Type) {
			case "text":
				if strings.TrimSpace(part.Text) != "" {
					msg.Blocks = append(msg.Blocks, PromptBlock{Type: PromptBlockText, Text: part.Text})
				}
			case "image":
				if strings.TrimSpace(part.URL) != "" || promptExtraString(part.Extra, "imageB64") != "" {
					msg.Blocks = append(msg.Blocks, PromptBlock{
						Type:     PromptBlockImage,
						ImageURL: part.URL,
						ImageB64: promptExtraString(part.Extra, "imageB64"),
						MimeType: part.MediaType,
					})
				}
			case "file":
				if strings.TrimSpace(part.URL) != "" || strings.TrimSpace(part.Filename) != "" || promptExtraString(part.Extra, "fileB64") != "" {
					msg.Blocks = append(msg.Blocks, PromptBlock{
						Type:     PromptBlockFile,
						FileURL:  part.URL,
						FileB64:  promptExtraString(part.Extra, "fileB64"),
						Filename: part.Filename,
						MimeType: part.MediaType,
					})
				}
			case "audio":
				if promptExtraString(part.Extra, "audioB64") != "" {
					msg.Blocks = append(msg.Blocks, PromptBlock{
						Type:        PromptBlockAudio,
						AudioB64:    promptExtraString(part.Extra, "audioB64"),
						AudioFormat: promptExtraString(part.Extra, "audioFormat"),
						MimeType:    part.MediaType,
					})
				}
			case "video":
				if strings.TrimSpace(part.URL) != "" || promptExtraString(part.Extra, "videoB64") != "" {
					msg.Blocks = append(msg.Blocks, PromptBlock{
						Type:     PromptBlockVideo,
						VideoURL: part.URL,
						VideoB64: promptExtraString(part.Extra, "videoB64"),
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
			switch normalizeTurnPartType(part.Type) {
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
						ToolCallArguments: CanonicalToolArguments(part.Input),
					})
				}
				outputText := strings.TrimSpace(FormatCanonicalValue(part.Output))
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

func TurnDataFromUserPromptMessages(messages []PromptMessage) (TurnData, bool) {
	if len(messages) == 0 {
		return TurnData{}, false
	}
	msg := messages[0]
	if msg.Role != PromptRoleUser {
		return TurnData{}, false
	}
	td := TurnData{Role: "user"}
	td.Parts = make([]TurnPart, 0, len(msg.Blocks))
	for _, block := range msg.Blocks {
		switch block.Type {
		case PromptBlockText:
			if strings.TrimSpace(block.Text) != "" {
				td.Parts = append(td.Parts, TurnPart{Type: "text", Text: block.Text})
			}
		case PromptBlockImage:
			if strings.TrimSpace(block.ImageURL) != "" || strings.TrimSpace(block.ImageB64) != "" {
				part := TurnPart{Type: "image", URL: block.ImageURL, MediaType: block.MimeType}
				if strings.TrimSpace(block.ImageB64) != "" {
					part.Extra = map[string]any{"imageB64": block.ImageB64}
				}
				td.Parts = append(td.Parts, part)
			}
		case PromptBlockFile:
			if strings.TrimSpace(block.FileURL) != "" || strings.TrimSpace(block.FileB64) != "" || strings.TrimSpace(block.Filename) != "" {
				part := TurnPart{
					Type:      "file",
					URL:       block.FileURL,
					Filename:  block.Filename,
					MediaType: block.MimeType,
				}
				if strings.TrimSpace(block.FileB64) != "" {
					part.Extra = map[string]any{"fileB64": block.FileB64}
				}
				td.Parts = append(td.Parts, part)
			}
		case PromptBlockAudio:
			if strings.TrimSpace(block.AudioB64) != "" {
				td.Parts = append(td.Parts, TurnPart{
					Type:      "audio",
					MediaType: block.MimeType,
					Extra: map[string]any{
						"audioB64":    block.AudioB64,
						"audioFormat": block.AudioFormat,
					},
				})
			}
		case PromptBlockVideo:
			if strings.TrimSpace(block.VideoURL) != "" || strings.TrimSpace(block.VideoB64) != "" {
				part := TurnPart{
					Type:      "video",
					URL:       block.VideoURL,
					MediaType: block.MimeType,
				}
				if strings.TrimSpace(block.VideoB64) != "" {
					part.Extra = map[string]any{"videoB64": block.VideoB64}
				}
				td.Parts = append(td.Parts, part)
			}
		}
	}
	return td, len(td.Parts) > 0
}

func CanonicalToolArguments(raw any) string {
	if value := strings.TrimSpace(FormatCanonicalValue(raw)); value != "" {
		return value
	}
	return "{}"
}

func FormatCanonicalValue(raw any) string {
	switch typed := raw.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}

func promptExtraString(extra map[string]any, key string) string {
	if len(extra) == 0 {
		return ""
	}
	value, _ := extra[key].(string)
	return strings.TrimSpace(value)
}
