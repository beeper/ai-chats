package sdk

import (
	"fmt"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// PromptContext is the canonical provider-facing prompt representation.
type PromptContext struct {
	SystemPrompt    string
	DeveloperPrompt string
	Messages        []PromptMessage
}

func UserPromptContext(blocks ...PromptBlock) PromptContext {
	return PromptContext{
		Messages: []PromptMessage{{
			Role:   PromptRoleUser,
			Blocks: slices.Clone(blocks),
		}},
	}
}

func PromptContextHasBlockType(ctx PromptContext, kinds ...PromptBlockType) bool {
	if len(kinds) == 0 {
		return false
	}
	allowed := make(map[PromptBlockType]struct{}, len(kinds))
	for _, kind := range kinds {
		allowed[kind] = struct{}{}
	}
	for _, msg := range ctx.Messages {
		for _, block := range msg.Blocks {
			if _, ok := allowed[block.Type]; ok {
				return true
			}
		}
	}
	return false
}

// ChatMessagesToPromptContext converts chat-completions-shaped messages into the canonical prompt model.
func ChatMessagesToPromptContext(messages []openai.ChatCompletionMessageParamUnion) PromptContext {
	var ctx PromptContext
	AppendChatMessagesToPromptContext(&ctx, messages)
	return ctx
}

func AppendChatMessagesToPromptContext(ctx *PromptContext, messages []openai.ChatCompletionMessageParamUnion) {
	if ctx == nil {
		return
	}
	for _, msg := range messages {
		appendChatMessageToPromptContext(ctx, msg)
	}
}

func appendChatMessageToPromptContext(ctx *PromptContext, msg openai.ChatCompletionMessageParamUnion) {
	if ctx == nil {
		return
	}
	switch {
	case msg.OfSystem != nil:
		AppendPromptText(&ctx.SystemPrompt, extractChatSystemText(msg.OfSystem.Content))
	case msg.OfDeveloper != nil:
		AppendPromptText(&ctx.DeveloperPrompt, extractChatDeveloperText(msg.OfDeveloper.Content))
	case msg.OfUser != nil:
		ctx.Messages = append(ctx.Messages, promptMessageFromChatUser(msg.OfUser))
	case msg.OfAssistant != nil:
		ctx.Messages = append(ctx.Messages, promptMessageFromChatAssistant(msg.OfAssistant))
	case msg.OfTool != nil:
		ctx.Messages = append(ctx.Messages, promptMessageFromChatTool(msg.OfTool))
	}
}

func AppendPromptText(dst *string, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if *dst == "" {
		*dst = text
		return
	}
	*dst = strings.TrimSpace(*dst + "\n\n" + text)
}

func promptMessageFromChatUser(msg *openai.ChatCompletionUserMessageParam) PromptMessage {
	pm := PromptMessage{Role: PromptRoleUser}
	if msg == nil {
		return pm
	}
	if msg.Content.OfString.Value != "" {
		pm.Blocks = append(pm.Blocks, PromptBlock{
			Type: PromptBlockText,
			Text: msg.Content.OfString.Value,
		})
	}
	for _, part := range msg.Content.OfArrayOfContentParts {
		pm.Blocks = append(pm.Blocks, promptBlockFromChatUserPart(part)...)
	}
	return pm
}

func promptBlockFromChatUserPart(part openai.ChatCompletionContentPartUnionParam) []PromptBlock {
	switch {
	case part.OfText != nil:
		return []PromptBlock{{Type: PromptBlockText, Text: part.OfText.Text}}
	case part.OfImageURL != nil:
		return []PromptBlock{{
			Type:     PromptBlockImage,
			ImageURL: part.OfImageURL.ImageURL.URL,
			MimeType: inferPromptMimeTypeFromDataURL(part.OfImageURL.ImageURL.URL),
		}}
	case part.OfFile != nil:
		return []PromptBlock{{
			Type:     PromptBlockFile,
			FileB64:  part.OfFile.File.FileData.Value,
			Filename: part.OfFile.File.Filename.Value,
		}}
	case part.OfInputAudio != nil:
		return []PromptBlock{{
			Type:        PromptBlockAudio,
			AudioB64:    part.OfInputAudio.InputAudio.Data,
			AudioFormat: part.OfInputAudio.InputAudio.Format,
		}}
	default:
		return nil
	}
}

func promptMessageFromChatAssistant(msg *openai.ChatCompletionAssistantMessageParam) PromptMessage {
	pm := PromptMessage{Role: PromptRoleAssistant}
	if msg == nil {
		return pm
	}
	if msg.Content.OfString.Value != "" {
		pm.Blocks = append(pm.Blocks, PromptBlock{
			Type: PromptBlockText,
			Text: msg.Content.OfString.Value,
		})
	}
	for _, part := range msg.Content.OfArrayOfContentParts {
		if part.OfText == nil {
			continue
		}
		pm.Blocks = append(pm.Blocks, PromptBlock{
			Type: PromptBlockText,
			Text: part.OfText.Text,
		})
	}
	for _, toolCall := range msg.ToolCalls {
		if toolCall.OfFunction == nil {
			continue
		}
		pm.Blocks = append(pm.Blocks, PromptBlock{
			Type:              PromptBlockToolCall,
			ToolCallID:        toolCall.OfFunction.ID,
			ToolName:          toolCall.OfFunction.Function.Name,
			ToolCallArguments: toolCall.OfFunction.Function.Arguments,
		})
	}
	return pm
}

func promptMessageFromChatTool(msg *openai.ChatCompletionToolMessageParam) PromptMessage {
	if msg == nil {
		return PromptMessage{Role: PromptRoleToolResult}
	}
	pm := PromptMessage{
		Role:       PromptRoleToolResult,
		ToolCallID: msg.ToolCallID,
	}
	if msg.Content.OfString.Value != "" {
		pm.Blocks = append(pm.Blocks, PromptBlock{
			Type: PromptBlockText,
			Text: msg.Content.OfString.Value,
		})
	}
	for _, part := range msg.Content.OfArrayOfContentParts {
		pm.Blocks = append(pm.Blocks, PromptBlock{
			Type: PromptBlockText,
			Text: part.Text,
		})
	}
	return pm
}

func extractChatSystemText(content openai.ChatCompletionSystemMessageParamContentUnion) string {
	if content.OfString.Value != "" {
		return content.OfString.Value
	}
	return joinChatText(content.OfArrayOfContentParts, func(part openai.ChatCompletionContentPartTextParam) string {
		return part.Text
	})
}

func extractChatDeveloperText(content openai.ChatCompletionDeveloperMessageParamContentUnion) string {
	if content.OfString.Value != "" {
		return content.OfString.Value
	}
	return joinChatText(content.OfArrayOfContentParts, func(part openai.ChatCompletionContentPartTextParam) string {
		return part.Text
	})
}

func joinChatText[T any](parts []T, extract func(T) string) string {
	var values []string
	for _, part := range parts {
		if text := strings.TrimSpace(extract(part)); text != "" {
			values = append(values, text)
		}
	}
	return strings.Join(values, "\n")
}

func inferPromptMimeTypeFromDataURL(value string) string {
	value = strings.TrimSpace(value)
	rest, ok := strings.CutPrefix(value, "data:")
	if !ok {
		return ""
	}
	value = rest
	idx := strings.Index(value, ";")
	if idx <= 0 {
		return ""
	}
	return value[:idx]
}

func BuildDataURL(mimeType, b64Data string) string {
	return fmt.Sprintf("data:%s;base64,%s", mimeType, b64Data)
}

// resolveBlockImageURL returns the image URL for a prompt block, falling back
// to a base64 data URL when no explicit URL is provided.
func resolveBlockImageURL(block PromptBlock) string {
	imageURL := strings.TrimSpace(block.ImageURL)
	if imageURL == "" && block.ImageB64 != "" {
		mimeType := block.MimeType
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		imageURL = BuildDataURL(mimeType, block.ImageB64)
	}
	return imageURL
}

// PromptContextToResponsesInput converts the canonical prompt model into Responses input items.
func PromptContextToResponsesInput(ctx PromptContext) responses.ResponseInputParam {
	var result responses.ResponseInputParam

	if strings.TrimSpace(ctx.DeveloperPrompt) != "" {
		result = append(result, responses.ResponseInputItemUnionParam{
			OfMessage: &responses.EasyInputMessageParam{
				Role: responses.EasyInputMessageRoleDeveloper,
				Content: responses.EasyInputMessageContentUnionParam{
					OfString: openai.String(ctx.DeveloperPrompt),
				},
			},
		})
	}

	for _, msg := range ctx.Messages {
		switch msg.Role {
		case PromptRoleUser:
			var contentParts responses.ResponseInputMessageContentListParam
			hasMultimodal := false
			textContent := ""

			for _, block := range msg.Blocks {
				switch block.Type {
				case PromptBlockText:
					if strings.TrimSpace(block.Text) == "" {
						continue
					}
					if textContent != "" {
						textContent += "\n"
					}
					textContent += block.Text
				case PromptBlockImage:
					imageURL := resolveBlockImageURL(block)
					if imageURL == "" {
						continue
					}
					hasMultimodal = true
					contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
						OfInputImage: &responses.ResponseInputImageParam{
							ImageURL: openai.String(imageURL),
							Detail:   responses.ResponseInputImageDetailAuto,
						},
					})
				case PromptBlockFile:
					fileData := strings.TrimSpace(block.FileB64)
					fileURL := strings.TrimSpace(block.FileURL)
					if fileData == "" && fileURL == "" {
						continue
					}
					hasMultimodal = true
					fileParam := &responses.ResponseInputFileParam{}
					if fileData != "" {
						fileParam.FileData = openai.String(fileData)
					}
					if fileURL != "" {
						fileParam.FileURL = openai.String(fileURL)
					}
					if strings.TrimSpace(block.Filename) != "" {
						fileParam.Filename = openai.String(block.Filename)
					}
					contentParts = append(contentParts, responses.ResponseInputContentUnionParam{
						OfInputFile: fileParam,
					})
				case PromptBlockAudio, PromptBlockVideo:
					// Unsupported in Responses API; caller should fall back to Chat Completions.
				}
			}

			if textContent != "" {
				textPart := responses.ResponseInputContentUnionParam{
					OfInputText: &responses.ResponseInputTextParam{Text: textContent},
				}
				contentParts = append([]responses.ResponseInputContentUnionParam{textPart}, contentParts...)
			}

			if hasMultimodal && len(contentParts) > 0 {
				result = append(result, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleUser,
						Content: responses.EasyInputMessageContentUnionParam{
							OfInputItemContentList: contentParts,
						},
					},
				})
			} else if textContent != "" {
				result = append(result, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role: responses.EasyInputMessageRoleUser,
						Content: responses.EasyInputMessageContentUnionParam{
							OfString: openai.String(textContent),
						},
					},
				})
			}
		case PromptRoleAssistant:
			textParts := make([]string, 0, len(msg.Blocks))
			for _, block := range msg.Blocks {
				switch block.Type {
				case PromptBlockText:
					if strings.TrimSpace(block.Text) != "" {
						textParts = append(textParts, block.Text)
					}
				case PromptBlockToolCall:
					callID := strings.TrimSpace(block.ToolCallID)
					name := strings.TrimSpace(block.ToolName)
					args := strings.TrimSpace(block.ToolCallArguments)
					if callID == "" || name == "" {
						continue
					}
					if args == "" {
						args = "{}"
					}
					result = appendAssistantTextItem(result, textParts)
					textParts = textParts[:0]
					result = append(result, responses.ResponseInputItemParamOfFunctionCall(args, callID, name))
				}
			}
			result = appendAssistantTextItem(result, textParts)
		case PromptRoleToolResult:
			callID := strings.TrimSpace(msg.ToolCallID)
			output := strings.TrimSpace(msg.Text())
			if callID == "" || output == "" {
				continue
			}
			result = append(result, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: callID,
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
						OfString: openai.String(output),
					},
				},
			})
		}
	}

	return result
}

func appendAssistantTextItem(result responses.ResponseInputParam, textParts []string) responses.ResponseInputParam {
	text := strings.TrimSpace(strings.Join(textParts, ""))
	if text == "" {
		return result
	}
	return append(result, responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role: responses.EasyInputMessageRoleAssistant,
			Content: responses.EasyInputMessageContentUnionParam{
				OfString: openai.String(text),
			},
		},
	})
}

// PromptContextToChatCompletionMessages converts the canonical prompt model into Chat Completions messages.
func PromptContextToChatCompletionMessages(ctx PromptContext, supportsVideoURL bool) []openai.ChatCompletionMessageParamUnion {
	result := make([]openai.ChatCompletionMessageParamUnion, 0, len(ctx.Messages)+2)
	if strings.TrimSpace(ctx.SystemPrompt) != "" {
		result = append(result, openai.SystemMessage(ctx.SystemPrompt))
	}
	if strings.TrimSpace(ctx.DeveloperPrompt) != "" {
		result = append(result, openai.ChatCompletionMessageParamUnion{
			OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
				Content: openai.ChatCompletionDeveloperMessageParamContentUnion{
					OfString: openai.String(ctx.DeveloperPrompt),
				},
			},
		})
	}

	for _, msg := range ctx.Messages {
		switch msg.Role {
		case PromptRoleUser:
			if promptMessageHasMultimodal(msg) {
				result = append(result, openai.ChatCompletionMessageParamUnion{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.ChatCompletionUserMessageParamContentUnion{
							OfArrayOfContentParts: promptBlocksToChatCompletionContentParts(msg.Blocks, supportsVideoURL),
						},
					},
				})
			} else {
				result = append(result, openai.UserMessage(msg.Text()))
			}
		case PromptRoleAssistant:
			assistant := &openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(msg.Text()),
				},
			}
			for _, block := range msg.Blocks {
				if block.Type != PromptBlockToolCall {
					continue
				}
				args := strings.TrimSpace(block.ToolCallArguments)
				if args == "" {
					args = "{}"
				}
				assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: block.ToolCallID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      block.ToolName,
							Arguments: args,
						},
						Type: "function",
					},
				})
			}
			result = append(result, openai.ChatCompletionMessageParamUnion{OfAssistant: assistant})
		case PromptRoleToolResult:
			result = append(result, openai.ToolMessage(msg.Text(), msg.ToolCallID))
		}
	}

	return result
}

func promptMessageHasMultimodal(msg PromptMessage) bool {
	for _, block := range msg.Blocks {
		switch block.Type {
		case PromptBlockImage, PromptBlockFile, PromptBlockAudio, PromptBlockVideo:
			return true
		}
	}
	return false
}

func promptBlocksToChatCompletionContentParts(blocks []PromptBlock, supportsVideoURL bool) []openai.ChatCompletionContentPartUnionParam {
	result := make([]openai.ChatCompletionContentPartUnionParam, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case PromptBlockText:
			if strings.TrimSpace(block.Text) == "" {
				continue
			}
			result = append(result, openai.ChatCompletionContentPartUnionParam{
				OfText: &openai.ChatCompletionContentPartTextParam{Text: block.Text},
			})
		case PromptBlockImage:
			imageURL := resolveBlockImageURL(block)
			if imageURL == "" {
				continue
			}
			result = append(result, openai.ChatCompletionContentPartUnionParam{
				OfImageURL: &openai.ChatCompletionContentPartImageParam{
					ImageURL: openai.ChatCompletionContentPartImageImageURLParam{URL: imageURL},
				},
			})
		case PromptBlockFile:
			file := openai.ChatCompletionContentPartFileFileParam{}
			if strings.TrimSpace(block.FileB64) != "" {
				file.FileData = param.NewOpt(block.FileB64)
			}
			if strings.TrimSpace(block.Filename) != "" {
				file.Filename = param.NewOpt(block.Filename)
			}
			result = append(result, openai.ChatCompletionContentPartUnionParam{
				OfFile: &openai.ChatCompletionContentPartFileParam{File: file},
			})
		case PromptBlockAudio:
			if strings.TrimSpace(block.AudioB64) == "" {
				continue
			}
			format := strings.TrimSpace(block.AudioFormat)
			if format == "" {
				format = "mp3"
			}
			result = append(result, openai.ChatCompletionContentPartUnionParam{
				OfInputAudio: &openai.ChatCompletionContentPartInputAudioParam{
					InputAudio: openai.ChatCompletionContentPartInputAudioInputAudioParam{
						Data:   block.AudioB64,
						Format: format,
					},
				},
			})
		case PromptBlockVideo:
			videoURL := strings.TrimSpace(block.VideoURL)
			if videoURL == "" && block.VideoB64 != "" {
				mimeType := strings.TrimSpace(block.MimeType)
				if mimeType == "" {
					mimeType = "video/mp4"
				}
				videoURL = BuildDataURL(mimeType, block.VideoB64)
			}
			if videoURL == "" {
				continue
			}
			if supportsVideoURL {
				result = append(result, param.Override[openai.ChatCompletionContentPartUnionParam](map[string]any{
					"type": "video_url",
					"video_url": map[string]any{
						"url": videoURL,
					},
				}))
			}
		}
	}
	return result
}

func HasUnsupportedResponsesPromptContext(ctx PromptContext) bool {
	return PromptContextHasBlockType(ctx, PromptBlockAudio, PromptBlockVideo)
}
