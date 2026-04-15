package ai

import (
	"fmt"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

func UserPromptContext(blocks ...PromptBlock) PromptContext {
	return PromptContext{
		Messages: []PromptMessage{{
			Role:   PromptRoleUser,
			Blocks: slices.Clone(blocks),
		}},
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

func BuildDataURL(mimeType, b64Data string) string {
	return fmt.Sprintf("data:%s;base64,%s", mimeType, b64Data)
}

func promptContextToResponsesInput(ctx PromptContext) responses.ResponseInputParam {
	var result responses.ResponseInputParam
	for _, msg := range ctx.Messages {
		switch msg.Role {
		case PromptRoleUser:
			content := make([]responses.ResponseInputContentUnionParam, 0, len(msg.Blocks))
			for _, block := range msg.Blocks {
				switch block.Type {
				case PromptBlockText:
					text := strings.TrimSpace(block.Text)
					if text == "" {
						continue
					}
					content = append(content, responses.ResponseInputContentUnionParam{
						OfInputText: &responses.ResponseInputTextParam{Text: text},
					})
				case PromptBlockImage:
					imageURL := strings.TrimSpace(block.ImageURL)
					if imageURL == "" && block.ImageB64 != "" {
						mimeType := strings.TrimSpace(block.MimeType)
						if mimeType == "" {
							mimeType = "image/jpeg"
						}
						imageURL = BuildDataURL(mimeType, block.ImageB64)
					}
					if imageURL == "" {
						continue
					}
					content = append(content, responses.ResponseInputContentUnionParam{
						OfInputImage: &responses.ResponseInputImageParam{
							ImageURL: param.NewOpt(imageURL),
						},
					})
				}
			}
			if len(content) == 0 {
				continue
			}
			result = append(result, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role:    responses.EasyInputMessageRoleUser,
					Content: responses.EasyInputMessageContentUnionParam{OfInputItemContentList: content},
				},
			})
		case PromptRoleAssistant:
			text := strings.TrimSpace(msg.VisibleText())
			if text != "" {
				result = append(result, responses.ResponseInputItemUnionParam{
					OfMessage: &responses.EasyInputMessageParam{
						Role:    responses.EasyInputMessageRoleAssistant,
						Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(text)},
					},
				})
			}
			for _, block := range msg.Blocks {
				if block.Type != PromptBlockToolCall || strings.TrimSpace(block.ToolCallID) == "" || strings.TrimSpace(block.ToolName) == "" {
					continue
				}
				args := strings.TrimSpace(block.ToolCallArguments)
				if args == "" {
					args = "{}"
				}
				result = append(result, responses.ResponseInputItemParamOfFunctionCall(args, block.ToolCallID, block.ToolName))
			}
		case PromptRoleToolResult:
			text := strings.TrimSpace(msg.Text())
			if strings.TrimSpace(msg.ToolCallID) == "" || text == "" {
				continue
			}
			result = append(result, buildFunctionCallOutputItem(msg.ToolCallID, text, false))
		}
	}
	return result
}

func promptContextToChatCompletionMessages(ctx PromptContext) []openai.ChatCompletionMessageParamUnion {
	var messages []openai.ChatCompletionMessageParamUnion
	if system := strings.TrimSpace(ctx.SystemPrompt); system != "" {
		messages = append(messages, openai.SystemMessage(system))
	}
	for _, msg := range ctx.Messages {
		switch msg.Role {
		case PromptRoleUser:
			user := promptUserToChatMessage(msg)
			if user != nil {
				messages = append(messages, openai.ChatCompletionMessageParamUnion{OfUser: user})
			}
		case PromptRoleAssistant:
			assistant := promptAssistantToChatMessage(msg)
			if assistant != nil {
				messages = append(messages, openai.ChatCompletionMessageParamUnion{OfAssistant: assistant})
			}
		case PromptRoleToolResult:
			tool := promptToolToChatMessage(msg)
			if tool != nil {
				messages = append(messages, openai.ChatCompletionMessageParamUnion{OfTool: tool})
			}
		}
	}
	return messages
}

func promptUserToChatMessage(msg PromptMessage) *openai.ChatCompletionUserMessageParam {
	var contentParts []openai.ChatCompletionContentPartUnionParam
	for _, block := range msg.Blocks {
		switch block.Type {
		case PromptBlockText:
			text := strings.TrimSpace(block.Text)
			if text == "" {
				continue
			}
			contentParts = append(contentParts, openai.ChatCompletionContentPartUnionParam{
				OfText: &openai.ChatCompletionContentPartTextParam{
					Text: text,
				},
			})
		case PromptBlockImage:
			imageURL := strings.TrimSpace(block.ImageURL)
			if imageURL == "" && block.ImageB64 != "" {
				mimeType := strings.TrimSpace(block.MimeType)
				if mimeType == "" {
					mimeType = "image/jpeg"
				}
				imageURL = BuildDataURL(mimeType, block.ImageB64)
			}
			if imageURL == "" {
				continue
			}
			contentParts = append(contentParts, openai.ChatCompletionContentPartUnionParam{
				OfImageURL: &openai.ChatCompletionContentPartImageParam{
					ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
						URL: imageURL,
					},
				},
			})
		}
	}
	if len(contentParts) == 0 {
		return nil
	}
	return &openai.ChatCompletionUserMessageParam{
		Content: openai.ChatCompletionUserMessageParamContentUnion{OfArrayOfContentParts: contentParts},
	}
}

func promptAssistantToChatMessage(msg PromptMessage) *openai.ChatCompletionAssistantMessageParam {
	var contentParts []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion
	var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
	for _, block := range msg.Blocks {
		switch block.Type {
		case PromptBlockText:
			text := strings.TrimSpace(block.Text)
			if text == "" {
				continue
			}
			contentParts = append(contentParts, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
				OfText: &openai.ChatCompletionContentPartTextParam{
					Text: text,
				},
			})
		case PromptBlockToolCall:
			if strings.TrimSpace(block.ToolCallID) == "" || strings.TrimSpace(block.ToolName) == "" {
				continue
			}
			args := strings.TrimSpace(block.ToolCallArguments)
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: block.ToolCallID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      block.ToolName,
						Arguments: args,
					},
				},
			})
		}
	}
	if len(contentParts) == 0 && len(toolCalls) == 0 {
		return nil
	}
	return &openai.ChatCompletionAssistantMessageParam{
		Content:   openai.ChatCompletionAssistantMessageParamContentUnion{OfArrayOfContentParts: contentParts},
		ToolCalls: toolCalls,
	}
}

func promptToolToChatMessage(msg PromptMessage) *openai.ChatCompletionToolMessageParam {
	text := strings.TrimSpace(msg.Text())
	if strings.TrimSpace(msg.ToolCallID) == "" || text == "" {
		return nil
	}
	return &openai.ChatCompletionToolMessageParam{
		ToolCallID: msg.ToolCallID,
		Content: openai.ChatCompletionToolMessageParamContentUnion{
			OfString: openai.String(text),
		},
	}
}

func chatMessagesToPromptContext(messages []openai.ChatCompletionMessageParamUnion) PromptContext {
	var ctx PromptContext
	for _, msg := range messages {
		switch {
		case msg.OfSystem != nil:
			if msg.OfSystem.Content.OfString.Value != "" {
				AppendPromptText(&ctx.SystemPrompt, msg.OfSystem.Content.OfString.Value)
				continue
			}
			var values []string
			for _, part := range msg.OfSystem.Content.OfArrayOfContentParts {
				if text := strings.TrimSpace(part.Text); text != "" {
					values = append(values, text)
				}
			}
			AppendPromptText(&ctx.SystemPrompt, strings.Join(values, "\n"))
		case msg.OfUser != nil:
			ctx.Messages = append(ctx.Messages, promptMessageFromChatUser(msg.OfUser))
		case msg.OfAssistant != nil:
			ctx.Messages = append(ctx.Messages, promptMessageFromChatAssistant(msg.OfAssistant))
		case msg.OfTool != nil:
			ctx.Messages = append(ctx.Messages, promptMessageFromChatTool(msg.OfTool))
		}
	}
	return ctx
}

func promptMessageFromChatUser(msg *openai.ChatCompletionUserMessageParam) PromptMessage {
	pm := PromptMessage{Role: PromptRoleUser}
	if msg == nil {
		return pm
	}
	if msg.Content.OfString.Value != "" {
		pm.Blocks = append(pm.Blocks, PromptBlock{Type: PromptBlockText, Text: msg.Content.OfString.Value})
	}
	for _, part := range msg.Content.OfArrayOfContentParts {
		switch {
		case part.OfText != nil:
			pm.Blocks = append(pm.Blocks, PromptBlock{Type: PromptBlockText, Text: part.OfText.Text})
		case part.OfImageURL != nil:
			mimeType := ""
			value := strings.TrimSpace(part.OfImageURL.ImageURL.URL)
			if rest, ok := strings.CutPrefix(value, "data:"); ok {
				if idx := strings.Index(rest, ";"); idx > 0 {
					mimeType = rest[:idx]
				}
			}
			pm.Blocks = append(pm.Blocks, PromptBlock{
				Type:     PromptBlockImage,
				ImageURL: part.OfImageURL.ImageURL.URL,
				MimeType: mimeType,
			})
		}
	}
	return pm
}

func promptMessageFromChatAssistant(msg *openai.ChatCompletionAssistantMessageParam) PromptMessage {
	pm := PromptMessage{Role: PromptRoleAssistant}
	if msg == nil {
		return pm
	}
	if msg.Content.OfString.Value != "" {
		pm.Blocks = append(pm.Blocks, PromptBlock{Type: PromptBlockText, Text: msg.Content.OfString.Value})
	}
	for _, part := range msg.Content.OfArrayOfContentParts {
		if part.OfText == nil {
			continue
		}
		pm.Blocks = append(pm.Blocks, PromptBlock{Type: PromptBlockText, Text: part.OfText.Text})
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
	pm := PromptMessage{Role: PromptRoleToolResult}
	if msg == nil {
		return pm
	}
	pm.ToolCallID = msg.ToolCallID
	if msg.Content.OfString.Value != "" {
		pm.Blocks = append(pm.Blocks, PromptBlock{Type: PromptBlockText, Text: msg.Content.OfString.Value})
	}
	for _, part := range msg.Content.OfArrayOfContentParts {
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		pm.Blocks = append(pm.Blocks, PromptBlock{Type: PromptBlockText, Text: part.Text})
	}
	return pm
}

func hasUnsupportedResponsesPromptContext(ctx PromptContext) bool {
	for _, msg := range ctx.Messages {
		for _, block := range msg.Blocks {
			switch block.Type {
			case PromptBlockText, PromptBlockImage, PromptBlockThinking, PromptBlockToolCall:
			default:
				return true
			}
		}
	}
	return false
}
