package connector

import (
	"context"

	"github.com/openai/openai-go/v3"
)

// applyProactivePruning applies context pruning before sending to the API
func (oc *AIClient) applyProactivePruning(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion, meta *PortalMetadata) []openai.ChatCompletionMessageParamUnion {
	_ = oc
	_ = ctx
	_ = meta
	return messages
}
