package ai

import (
	"context"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

func TestAgentLoopRequestBuildersShareModelAndTokenSettings(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				DefaultSystemPrompt: "system prompt",
			},
		},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			Provider: ProviderOpenRouter,
			ModelCache: &ModelCache{Models: []ModelInfo{{
				ID:                "openai/gpt-5.2",
				MaxOutputTokens:   777,
				SupportsReasoning: true,
			}}},
		}}},
	}
	meta := &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetModel,
			ModelID: "openai/gpt-5.2",
		},
	}

	chatParams := oc.buildChatCompletionsAgentLoopParams(context.Background(), meta, []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("hello"),
	})
	responsesParams := oc.buildResponsesAgentLoopParams(context.Background(), meta, "system prompt", nil, false)

	if chatParams.Model != "openai/gpt-5.2" {
		t.Fatalf("expected chat model openai/gpt-5.2, got %q", chatParams.Model)
	}
	if string(responsesParams.Model) != "openai/gpt-5.2" {
		t.Fatalf("expected responses model openai/gpt-5.2, got %q", responsesParams.Model)
	}
	if chatParams.MaxCompletionTokens.Value != 777 {
		t.Fatalf("expected chat max completion tokens 777, got %d", chatParams.MaxCompletionTokens.Value)
	}
	if responsesParams.MaxOutputTokens.Value != 777 {
		t.Fatalf("expected responses max output tokens 777, got %d", responsesParams.MaxOutputTokens.Value)
	}
	if chatParams.StreamOptions.IncludeUsage.Value != true {
		t.Fatalf("expected chat stream options to include usage")
	}
	if responsesParams.Instructions.Value != "system prompt" {
		t.Fatalf("expected responses instructions to use shared system prompt, got %q", responsesParams.Instructions.Value)
	}
	if responsesParams.Reasoning.Effort != shared.ReasoningEffortLow {
		t.Fatalf("expected responses reasoning effort low, got %q", responsesParams.Reasoning.Effort)
	}
}

func TestAgentLoopRequestBuildersPreserveExplicitZeroTemperature(t *testing.T) {
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				DefaultSystemPrompt: "system prompt",
			},
		},
		UserLogin: &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{
			Provider: ProviderOpenRouter,
			CustomAgents: map[string]*AgentDefinitionContent{
				"agent-1": {
					ID:          "agent-1",
					Name:        "Agent One",
					Model:       "openai/gpt-5.2",
					Temperature: ptr.Ptr(0.0),
				},
			},
			ModelCache: &ModelCache{Models: []ModelInfo{{
				ID:                "openai/gpt-5.2",
				MaxOutputTokens:   777,
				SupportsReasoning: true,
			}}},
		}}},
	}
	meta := &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetAgent,
			AgentID: "agent-1",
		},
	}

	chatParams := oc.buildChatCompletionsAgentLoopParams(context.Background(), meta, []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("hello"),
	})
	responsesParams := oc.buildResponsesAgentLoopParams(context.Background(), meta, "system prompt", nil, false)

	if !chatParams.Temperature.Valid() || chatParams.Temperature.Value != 0 {
		t.Fatalf("expected explicit zero chat temperature, got %#v", chatParams.Temperature)
	}
	if !responsesParams.Temperature.Valid() || responsesParams.Temperature.Value != 0 {
		t.Fatalf("expected explicit zero responses temperature, got %#v", responsesParams.Temperature)
	}
}
