package ai

import (
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared/constant"
)

func TestConvertToResponsesInput_RolesAndToolOutput(t *testing.T) {
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("hello"),
		openai.ToolMessage("tool output", "call_123"),
	}

	input := PromptContextToResponsesInput(ChatMessagesToPromptContext(messages))
	if len(input) != 2 {
		t.Fatalf("expected 2 input items, got %d", len(input))
	}

	if input[0].OfMessage == nil || input[0].OfMessage.Role != responses.EasyInputMessageRoleUser {
		t.Fatalf("expected user message input first")
	}

	if input[1].OfFunctionCallOutput == nil {
		t.Fatalf("expected function_call_output input second")
	}
	if input[1].OfFunctionCallOutput.CallID != "call_123" {
		t.Fatalf("expected call_id call_123, got %s", input[1].OfFunctionCallOutput.CallID)
	}
	if input[1].OfFunctionCallOutput.Output.OfString.Value != "tool output" {
		t.Fatalf("expected tool output to match, got %q", input[1].OfFunctionCallOutput.Output.OfString.Value)
	}
}

func TestConvertToResponsesInput_AssistantToolCalls(t *testing.T) {
	messages := []openai.ChatCompletionMessageParamUnion{{
		OfAssistant: &openai.ChatCompletionAssistantMessageParam{
			Content: openai.ChatCompletionAssistantMessageParamContentUnion{
				OfString: openai.String("thinking"),
			},
			ToolCalls: []openai.ChatCompletionMessageToolCallUnionParam{{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: "call_123",
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      "search",
						Arguments: "{\"query\":\"matrix\"}",
					},
					Type: constant.ValueOf[constant.Function](),
				},
			}},
		},
	}}

	input := PromptContextToResponsesInput(ChatMessagesToPromptContext(messages))
	if len(input) != 2 {
		t.Fatalf("expected 2 input items, got %d", len(input))
	}
	if input[0].OfMessage == nil || input[0].OfMessage.Role != responses.EasyInputMessageRoleAssistant {
		t.Fatalf("expected assistant message input first")
	}
	if input[1].OfFunctionCall == nil {
		t.Fatalf("expected function_call input second")
	}
	if input[1].OfFunctionCall.CallID != "call_123" {
		t.Fatalf("expected call_123, got %q", input[1].OfFunctionCall.CallID)
	}
	if input[1].OfFunctionCall.Name != "search" {
		t.Fatalf("expected search tool, got %q", input[1].OfFunctionCall.Name)
	}
}
