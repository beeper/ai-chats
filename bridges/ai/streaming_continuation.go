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
		if len(steeringMessages) > 0 {
			input = append(input, promptContextToResponsesInput(PromptContext{Messages: steeringMessages})...)
		}
	}
	systemPrompt := ""
	if prompt != nil {
		systemPrompt = prompt.SystemPrompt
	}
	return oc.buildResponsesAgentLoopParams(ctx, meta, systemPrompt, input, true)
}
