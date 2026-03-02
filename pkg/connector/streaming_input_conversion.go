package connector

import (
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

func (oc *AIClient) convertToResponsesInput(messages []openai.ChatCompletionMessageParamUnion, _ *PortalMetadata) responses.ResponseInputParam {
	var input responses.ResponseInputParam

	for _, msg := range messages {
		if msg.OfTool != nil {
			toolCallID := strings.TrimSpace(msg.OfTool.ToolCallID)
			content := strings.TrimSpace(airuntime.ExtractToolContent(msg.OfTool.Content))
			if toolCallID != "" && content != "" {
				input = append(input, responses.ResponseInputItemUnionParam{
					OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
						CallID: toolCallID,
						Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
							OfString: openai.String(content),
						},
					},
				})
			}
			continue
		}

		if msg.OfUser != nil {
			var contentParts responses.ResponseInputMessageContentListParam
			hasMultimodal := false
			textContent := ""

			if msg.OfUser.Content.OfString.Value != "" {
				textContent = msg.OfUser.Content.OfString.Value
			}

			if len(msg.OfUser.Content.OfArrayOfContentParts) > 0 {
				for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
					if part.OfText != nil && part.OfText.Text != "" {
						if textContent != "" {
							textContent += "\n"
						}
						textContent += part.OfText.Text
					}
					if part.OfImageURL != nil && part.OfImageURL.ImageURL.URL != "" {
						hasMultimodal = true
						detail := responses.ResponseInputImageDetailAuto
						switch part.OfImageURL.ImageURL.Detail {
						case "low":
							detail = responses.ResponseInputImageDetailLow
						case "high":
							detail = responses.ResponseInputImageDetailHigh
						}
						contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
							OfInputImage: &responses.ResponseInputImageParam{
								ImageURL: openai.String(part.OfImageURL.ImageURL.URL),
								Detail:   detail,
							},
						})
					}
					if part.OfFile != nil {
						fileData := part.OfFile.File.FileData.Value
						fileID := part.OfFile.File.FileID.Value
						filename := part.OfFile.File.Filename.Value
						if fileData == "" && fileID == "" {
							continue
						}
						hasMultimodal = true
						fileParam := &responses.ResponseInputFileParam{}
						if fileData != "" {
							fileParam.FileData = openai.String(fileData)
						}
						if fileID != "" {
							fileParam.FileID = openai.String(fileID)
						}
						if filename != "" {
							fileParam.Filename = openai.String(filename)
						}
						contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
							OfInputFile: fileParam,
						})
					}
					// Note: Audio handled by Chat Completions fallback, skip here
				}
			}

			if textContent != "" {
				textPart := responses.ResponseInputContentUnionParam{
					OfInputText: &responses.ResponseInputTextParam{
						Text: textContent,
					},
				}
				contentParts = append([]responses.ResponseInputContentUnionParam{textPart}, contentParts...)
			}

			if hasMultimodal && len(contentParts) > 0 {
				input = append(input, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleUser,
						Content: responses.EasyInputMessageContentUnionParam{
							OfInputItemContentList: contentParts,
						},
					},
				})
			} else if textContent != "" {
				input = append(input, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleUser,
						Content: responses.EasyInputMessageContentUnionParam{
							OfString: openai.String(textContent),
						},
					},
				})
			}
			continue
		}

		content, role := airuntime.ExtractMessageContent(msg)
		if role == "" || content == "" {
			continue
		}

		var responsesRole responses.EasyInputMessageRole
		switch role {
		case "system":
			responsesRole = responses.EasyInputMessageRoleSystem
		case "developer":
			responsesRole = responses.EasyInputMessageRoleDeveloper
		case "assistant":
			responsesRole = responses.EasyInputMessageRoleAssistant
		case "user":
			responsesRole = responses.EasyInputMessageRoleUser
		default:
			continue
		}

		input = append(input, responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role: responsesRole,
				Content: responses.EasyInputMessageContentUnionParam{
					OfString: openai.String(content),
				},
			},
		})
	}

	return input
}

// hasAudioContent checks if the prompt contains audio content
func hasAudioContent(messages []openai.ChatCompletionMessageParamUnion) bool {
	for _, msg := range messages {
		if msg.OfUser != nil && len(msg.OfUser.Content.OfArrayOfContentParts) > 0 {
			for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
				if part.OfInputAudio != nil {
					return true
				}
			}
		}
	}
	return false
}

// hasMultimodalContent checks if the prompt contains non-text content (image, file, audio).
func hasMultimodalContent(messages []openai.ChatCompletionMessageParamUnion) bool {
	for _, msg := range messages {
		if msg.OfUser != nil && len(msg.OfUser.Content.OfArrayOfContentParts) > 0 {
			for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
				if part.OfImageURL != nil || part.OfFile != nil || part.OfInputAudio != nil {
					return true
				}
			}
		}
	}
	return false
}
