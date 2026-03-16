package ai

import (
	"context"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

// buildContinuationParams builds params for continuing a response after tool execution
// and/or after responding to tool approval requests.
func (oc *AIClient) buildContinuationParams(
	ctx context.Context,
	state *streamingState,
	meta *PortalMetadata,
	pendingOutputs []functionCallOutput,
	approvalInputs []responses.ResponseInputItemUnionParam,
) responses.ResponseNewParams {
	// Build function call outputs as input
	var input responses.ResponseInputParam
	if len(state.baseInput) > 0 {
		// All Responses continuations are stateless: include the accumulated local history.
		input = append(input, state.baseInput...)
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
		steerInput := oc.buildSteeringInputItems(steerPrompts, meta)
		if len(steerInput) > 0 {
			input = append(input, steerInput...)
			state.baseInput = append(state.baseInput, steerInput...)
		}
	}
	return oc.buildResponsesAgentLoopParams(ctx, meta, input, true)
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
		messages := []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)}
		input = append(input, oc.convertToResponsesInput(messages, meta)...)
	}
	return input
}
