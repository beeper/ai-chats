package connector

import (
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

func TestConvertToResponsesInput_RolesAndToolOutput(t *testing.T) {
	oc := &AIClient{}

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.DeveloperMessage("dev instructions"),
		openai.UserMessage("hello"),
		openai.ToolMessage("tool output", "call_123"),
	}

	input := oc.convertToResponsesInput(messages, nil)
	if len(input) != 3 {
		t.Fatalf("expected 3 input items, got %d", len(input))
	}

	if input[0].OfMessage == nil {
		t.Fatalf("expected developer message input, got nil")
	}
	if input[0].OfMessage.Role != responses.EasyInputMessageRoleDeveloper {
		t.Fatalf("expected developer role, got %s", input[0].OfMessage.Role)
	}

	if input[1].OfMessage == nil || input[1].OfMessage.Role != responses.EasyInputMessageRoleUser {
		t.Fatalf("expected user message input for item 2")
	}

	if input[2].OfFunctionCallOutput == nil {
		t.Fatalf("expected function_call_output input for item 3")
	}
	if input[2].OfFunctionCallOutput.CallID != "call_123" {
		t.Fatalf("expected call_id call_123, got %s", input[2].OfFunctionCallOutput.CallID)
	}
	if input[2].OfFunctionCallOutput.Output.OfString.Value != "tool output" {
		t.Fatalf("expected tool output to match, got %q", input[2].OfFunctionCallOutput.Output.OfString.Value)
	}
}
