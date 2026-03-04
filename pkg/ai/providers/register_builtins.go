package providers

import (
	"time"

	"github.com/beeper/ai-bridge/pkg/ai"
)

const BuiltinProviderSourceID = "pkg/ai/providers/register_builtins"

func notImplementedStream(apiID ai.Api) ai.StreamFn {
	return func(model ai.Model, _ ai.Context, _ *ai.StreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream(2)
		stream.Push(ai.AssistantMessageEvent{
			Type: ai.EventError,
			Error: ai.Message{
				Role:         ai.RoleAssistant,
				API:          apiID,
				Provider:     model.Provider,
				Model:        model.ID,
				StopReason:   ai.StopReasonError,
				ErrorMessage: "provider runtime is not implemented yet",
				Timestamp:    time.Now().UnixMilli(),
			},
			Reason: ai.StopReasonError,
		})
		return stream
	}
}

func notImplementedSimpleStream(apiID ai.Api) ai.StreamSimpleFn {
	return func(model ai.Model, _ ai.Context, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream(2)
		stream.Push(ai.AssistantMessageEvent{
			Type: ai.EventError,
			Error: ai.Message{
				Role:         ai.RoleAssistant,
				API:          apiID,
				Provider:     model.Provider,
				Model:        model.ID,
				StopReason:   ai.StopReasonError,
				ErrorMessage: "provider runtime is not implemented yet",
				Timestamp:    time.Now().UnixMilli(),
			},
			Reason: ai.StopReasonError,
		})
		return stream
	}
}

// RegisterBuiltInAPIProviders registers providers implemented in this package.
func RegisterBuiltInAPIProviders() {
	ai.RegisterAPIProvider(ai.APIProvider{
		API:          ai.APIOpenAIResponses,
		Stream:       streamOpenAIResponses,
		StreamSimple: streamSimpleOpenAIResponses,
	}, BuiltinProviderSourceID)
	ai.RegisterAPIProvider(ai.APIProvider{
		API:          ai.APIOpenAICompletions,
		Stream:       streamOpenAICompletions,
		StreamSimple: streamSimpleOpenAICompletions,
	}, BuiltinProviderSourceID)
	ai.RegisterAPIProvider(ai.APIProvider{
		API:          ai.APIAzureOpenAIResponse,
		Stream:       streamAzureOpenAIResponses,
		StreamSimple: streamSimpleAzureOpenAIResponses,
	}, BuiltinProviderSourceID)
	ai.RegisterAPIProvider(ai.APIProvider{
		API:          ai.APIOpenAICodexResponse,
		Stream:       streamOpenAICodexResponses,
		StreamSimple: streamSimpleOpenAICodexResponses,
	}, BuiltinProviderSourceID)
	ai.RegisterAPIProvider(ai.APIProvider{
		API:          ai.APIAnthropicMessages,
		Stream:       streamAnthropicMessages,
		StreamSimple: streamSimpleAnthropicMessages,
	}, BuiltinProviderSourceID)
	ai.RegisterAPIProvider(ai.APIProvider{
		API:          ai.APIGoogleGenerativeAI,
		Stream:       streamGoogleGenerativeAI,
		StreamSimple: streamSimpleGoogleGenerativeAI,
	}, BuiltinProviderSourceID)
	ai.RegisterAPIProvider(ai.APIProvider{
		API:          ai.APIGoogleVertex,
		Stream:       streamGoogleVertex,
		StreamSimple: streamSimpleGoogleVertex,
	}, BuiltinProviderSourceID)

	for _, apiID := range []ai.Api{
		ai.APIGoogleGeminiCLI,
		ai.APIBedrockConverse,
	} {
		ai.RegisterAPIProvider(ai.APIProvider{
			API:          apiID,
			Stream:       notImplementedStream(apiID),
			StreamSimple: notImplementedSimpleStream(apiID),
		}, BuiltinProviderSourceID)
	}
}

func ResetAPIProviders() {
	ai.ClearAPIProviders()
	RegisterBuiltInAPIProviders()
}
