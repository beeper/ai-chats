package ai

import (
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	bridgesdk "github.com/beeper/agentremote/sdk"
)

func (oc *AIClient) convertToResponsesInput(messages []openai.ChatCompletionMessageParamUnion, _ *PortalMetadata) responses.ResponseInputParam {
	return bridgesdk.PromptContextToResponsesInput(bridgesdk.ChatMessagesToPromptContext(messages))
}

// hasAudioContent checks if the prompt contains audio content
func hasAudioContent(messages []openai.ChatCompletionMessageParamUnion) bool {
	for _, msg := range messages {
		if msg.OfUser != nil && len(msg.OfUser.Content.OfArrayOfContentParts) > 0 {
			for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
				if part.OfInputAudio != nil {
					return true
				}
			}
		}
	}
	return false
}

// hasMultimodalContent checks if the prompt contains non-text content (image, file, audio).
func hasMultimodalContent(messages []openai.ChatCompletionMessageParamUnion) bool {
	for _, msg := range messages {
		if msg.OfUser != nil && len(msg.OfUser.Content.OfArrayOfContentParts) > 0 {
			for _, part := range msg.OfUser.Content.OfArrayOfContentParts {
				if part.OfImageURL != nil || part.OfFile != nil || part.OfInputAudio != nil {
					return true
				}
			}
		}
	}
	return false
}
