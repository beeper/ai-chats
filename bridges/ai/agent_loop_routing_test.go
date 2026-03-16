package ai

import (
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	bridgesdk "github.com/beeper/agentremote/sdk"
)

func newAgentLoopRoutingTestClient(models ...ModelInfo) *AIClient {
	login := &database.UserLogin{
		ID: networkid.UserLoginID("login"),
		Metadata: &UserLoginMetadata{
			Provider: ProviderOpenAI,
			ModelCache: &ModelCache{
				Models: models,
			},
		},
	}
	return &AIClient{
		UserLogin: &bridgev2.UserLogin{
			UserLogin: login,
			Log:       zerolog.Nop(),
		},
		log: zerolog.Nop(),
	}
}

func resolvedModelMeta(modelID string) *PortalMetadata {
	return &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetModel,
			ModelID: modelID,
		},
	}
}

func TestSelectAgentLoopRunFunc_UsesChatCompletionsForUnsupportedResponsesPromptContext(t *testing.T) {
	oc := newAgentLoopRoutingTestClient(ModelInfo{
		ID:  "openai/gpt-4.1",
		API: string(ModelAPIResponses),
	})

	promptContext := PromptContext{
		PromptContext: bridgesdk.UserPromptContext(bridgesdk.PromptBlock{
			Type:        bridgesdk.PromptBlockAudio,
			AudioB64:    "YXVkaW8=",
			AudioFormat: "mp3",
		}),
	}

	responseFn, logLabel := oc.selectAgentLoopRunFunc(resolvedModelMeta("openai/gpt-4.1"), promptContext)
	if responseFn == nil {
		t.Fatal("expected non-nil response function")
	}
	if logLabel != "chat_completions" {
		t.Fatalf("expected chat_completions label, got %q", logLabel)
	}
}

func TestSelectAgentLoopRunFunc_UsesChatCompletionsForChatModelAPI(t *testing.T) {
	oc := newAgentLoopRoutingTestClient(ModelInfo{
		ID:  "anthropic/claude-3.7-sonnet",
		API: string(ModelAPIChatCompletions),
	})

	meta := resolvedModelMeta("anthropic/claude-3.7-sonnet")
	if got := oc.resolveModelAPI(meta); got != ModelAPIChatCompletions {
		t.Fatalf("expected chat_completions model API, got %q", got)
	}

	responseFn, logLabel := oc.selectAgentLoopRunFunc(meta, PromptContext{})
	if responseFn == nil {
		t.Fatal("expected non-nil response function")
	}
	if logLabel != "chat_completions" {
		t.Fatalf("expected chat_completions label, got %q", logLabel)
	}
}

func TestSelectAgentLoopRunFunc_DefaultsToResponses(t *testing.T) {
	oc := newAgentLoopRoutingTestClient(ModelInfo{
		ID:  "openai/gpt-4.1",
		API: string(ModelAPIResponses),
	})

	meta := resolvedModelMeta("openai/gpt-4.1")
	if got := oc.resolveModelAPI(meta); got != ModelAPIResponses {
		t.Fatalf("expected responses model API, got %q", got)
	}

	responseFn, logLabel := oc.selectAgentLoopRunFunc(meta, PromptContext{})
	if responseFn == nil {
		t.Fatal("expected non-nil response function")
	}
	if logLabel != "responses" {
		t.Fatalf("expected responses label, got %q", logLabel)
	}
}
