package connector

import (
	"context"
	"strings"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
)

// applyProactivePruning applies context pruning before sending to the API
func (oc *AIClient) applyProactivePruning(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion, meta *PortalMetadata) []openai.ChatCompletionMessageParamUnion {
	config := oc.connector.Config.Pruning
	if config == nil {
		return messages
	}
	if strings.EqualFold(strings.TrimSpace(config.Mode), "off") || !config.Enabled {
		return messages
	}

	// Get model context window (default to 128k if unknown)
	contextWindow := oc.getModelContextWindow(meta)
	if contextWindow <= 0 {
		contextWindow = 128000
	}

	log := zerolog.Ctx(ctx)
	beforeCount := len(messages)

	pruned := airuntime.PruneContext(messages, config, contextWindow)

	if len(pruned) != beforeCount {
		log.Debug().
			Int("before", beforeCount).
			Int("after", len(pruned)).
			Int("context_window", contextWindow).
			Msg("Applied proactive context pruning")
	}

	return pruned
}
