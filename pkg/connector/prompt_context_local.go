package connector

import (
	"fmt"
	"slices"
	"strings"

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
						Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(text)},
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
