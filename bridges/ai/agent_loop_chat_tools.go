package ai

import (
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared/constant"
)

func executeChatToolCallsSequentially(
	keys []string,
	activeTools *streamToolRegistry,
	executeTool func(tool *activeToolCall, toolName, argsJSON string),
	getSteeringMessages func() []string,
) ([]openai.ChatCompletionMessageToolCallUnionParam, []string) {
	toolCallParams := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(keys))
	for _, key := range keys {
		tool := activeTools.Lookup(key)
		if tool == nil {
			continue
		}
		if tool.callID == "" {
			tool.callID = NewCallID()
		}
		toolName := strings.TrimSpace(tool.toolName)
		if toolName == "" {
			toolName = "unknown_tool"
		}
		tool.toolName = toolName

		argsJSON := normalizeToolArgsJSON(tool.input.String())
		toolCallParams = append(toolCallParams, openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID: tool.callID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      toolName,
					Arguments: argsJSON,
				},
				Type: constant.ValueOf[constant.Function](),
			},
		})
		if executeTool != nil {
			executeTool(tool, toolName, argsJSON)
		}
		if getSteeringMessages != nil {
			if steeringMessages := getSteeringMessages(); len(steeringMessages) > 0 {
				return toolCallParams, steeringMessages
			}
		}
	}
	return toolCallParams, nil
}
