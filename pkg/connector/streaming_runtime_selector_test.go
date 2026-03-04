package connector

import (
	"testing"

	"github.com/openai/openai-go/v3"
)

func TestPkgAIRuntimeEnabledFromEnv(t *testing.T) {
	t.Setenv("PI_USE_PKG_AI_RUNTIME", "")
	if pkgAIRuntimeEnabled() {
		t.Fatalf("expected runtime flag disabled by default")
	}

	t.Setenv("PI_USE_PKG_AI_RUNTIME", "1")
	if !pkgAIRuntimeEnabled() {
		t.Fatalf("expected runtime flag enabled for value 1")
	}

	t.Setenv("PI_USE_PKG_AI_RUNTIME", "true")
	if !pkgAIRuntimeEnabled() {
		t.Fatalf("expected runtime flag enabled for value true")
	}

	t.Setenv("PI_USE_PKG_AI_RUNTIME", "off")
	if pkgAIRuntimeEnabled() {
		t.Fatalf("expected runtime flag disabled for value off")
	}
}

func TestChooseStreamingRuntimePath(t *testing.T) {
	if got := chooseStreamingRuntimePath(true, ModelAPIResponses, true); got != streamingRuntimeChatCompletions {
		t.Fatalf("expected audio to force chat completions, got %s", got)
	}
	if got := chooseStreamingRuntimePath(false, ModelAPIResponses, true); got != streamingRuntimePkgAI {
		t.Fatalf("expected pkg_ai path when preferred and no audio, got %s", got)
	}
	if got := chooseStreamingRuntimePath(false, ModelAPIChatCompletions, false); got != streamingRuntimeChatCompletions {
		t.Fatalf("expected chat model api path, got %s", got)
	}
	if got := chooseStreamingRuntimePath(false, ModelAPIResponses, false); got != streamingRuntimeResponses {
		t.Fatalf("expected responses path fallback, got %s", got)
	}
}

func TestChatPromptToUnifiedMessages_ConvertsRolesAndImages(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("system guidance"),
		{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
						{
							OfText: &openai.ChatCompletionContentPartTextParam{
								Text: "look at image",
							},
						},
						{
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL: "https://example.com/image.png",
								},
							},
						},
					},
				},
			},
		},
		openai.AssistantMessage("ack"),
		openai.ToolMessage("tool output", "call_1"),
	}

	unified := chatPromptToUnifiedMessages(prompt)
	if len(unified) != 3 {
		t.Fatalf("expected three non-system unified messages, got %d", len(unified))
	}
	if unified[0].Role != RoleUser {
		t.Fatalf("expected first role user, got %s", unified[0].Role)
	}
	if len(unified[0].Content) < 2 || unified[0].Content[1].Type != ContentTypeImage {
		t.Fatalf("expected user message to include image content part, got %#v", unified[0].Content)
	}
	if unified[1].Role != RoleAssistant || unified[1].Text() != "ack" {
		t.Fatalf("expected assistant text mapping, got %#v", unified[1])
	}
	if unified[2].Role != RoleTool || unified[2].ToolCallID != "call_1" {
		t.Fatalf("expected tool mapping with tool_call_id, got %#v", unified[2])
	}
}

func TestBuildPkgAIContext_UsesSystemPromptAndMappedMessages(t *testing.T) {
	prompt := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("inline system"),
		openai.UserMessage("hello"),
		openai.AssistantMessage("hi"),
	}
	ctx := buildPkgAIContext("effective system prompt", prompt)
	if ctx.SystemPrompt != "effective system prompt" {
		t.Fatalf("expected explicit effective system prompt in ai context, got %q", ctx.SystemPrompt)
	}
	if len(ctx.Messages) != 2 {
		t.Fatalf("expected 2 mapped messages (system stripped), got %d", len(ctx.Messages))
	}
	if ctx.Messages[0].Role != "user" || ctx.Messages[1].Role != "assistant" {
		t.Fatalf("unexpected mapped roles: %#v", ctx.Messages)
	}
}
