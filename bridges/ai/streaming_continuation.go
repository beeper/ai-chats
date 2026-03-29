package ai

import (
	"context"
	"strings"

	"github.com/openai/openai-go/v3/responses"
)

// buildContinuationParams builds params for continuing a response after tool execution
// and/or after responding to tool approval requests.
func (oc *AIClient) buildContinuationParams(
	ctx context.Context,
	prompt *PromptContext,
	state *streamingState,
	meta *PortalMetadata,
	pendingOutputs []functionCallOutput,
	approvalInputs []responses.ResponseInputItemUnionParam,
) responses.ResponseNewParams {
	var input responses.ResponseInputParam
	if prompt != nil {
		input = append(input, promptContextToResponsesInput(*prompt)...)
	}
	input = append(input, approvalInputs...)
	for _, output := range pendingOutputs {
		if output.name != "" {
			args := output.arguments
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			input = append(input, responses.ResponseInputItemParamOfFunctionCall(args, output.callID, output.name))
		}
		input = append(input, buildFunctionCallOutputItem(output.callID, output.output, oc.isOpenRouterProvider()))
	}
	steerPrompts := state.consumePendingSteeringPrompts()
	if len(steerPrompts) == 0 {
		steerPrompts = oc.getSteeringMessages(state.roomID)
	}
	if len(steerPrompts) > 0 {
		steeringMessages := buildSteeringPromptMessages(steerPrompts)
		if prompt != nil && len(steeringMessages) > 0 {
			prompt.Messages = append(prompt.Messages, steeringMessages...)
		}
		steerInput := oc.buildSteeringInputItems(steerPrompts, meta)
		if len(steerInput) > 0 {
			input = append(input, steerInput...)
		}
	}
	systemPrompt := ""
	if prompt != nil {
		systemPrompt = prompt.SystemPrompt
	}
	return oc.buildResponsesAgentLoopParams(ctx, meta, systemPrompt, input, true)
}

func (oc *AIClient) buildSteeringInputItems(prompts []string, meta *PortalMetadata) responses.ResponseInputParam {
	if oc == nil || len(prompts) == 0 {
		return nil
	}
	var input responses.ResponseInputParam
	for _, prompt := range prompts {
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			continue
		}
		input = append(input, promptContextToResponsesInput(UserPromptContext(
			PromptBlock{Type: PromptBlockText, Text: prompt},
		))...)
	}
	return input
}
