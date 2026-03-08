package connector

import (
	"context"
	"errors"
	"strings"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

// NonFallbackError marks an error as ineligible for model fallback.
// This is used when partial output has already been sent.
type NonFallbackError struct {
	Err error
}

func (e *NonFallbackError) Error() string {
	return e.Err.Error()
}

func (e *NonFallbackError) Unwrap() error {
	return e.Err
}

// modelFallbackChain returns the model chain to try in order.
// Agent-defined fallbacks are used for agent rooms; model rooms only use their selected model.
func (oc *AIClient) modelFallbackChain(ctx context.Context, meta *PortalMetadata) []string {
	agentID := resolveAgentID(meta)
	if agentID != "" {
		store := NewAgentStoreAdapter(oc)
		agent, err := store.GetAgentByID(ctx, agentID)
		if err == nil && agent != nil {
			var models []string
			if strings.TrimSpace(agent.Model.Primary) != "" {
				models = append(models, ResolveAlias(agent.Model.Primary))
			}
			for _, fb := range agent.Model.Fallbacks {
				if strings.TrimSpace(fb) == "" {
					continue
				}
				models = append(models, ResolveAlias(fb))
			}
			return dedupeModels(models)
		}
	}

	// No agent fallbacks - use the effective model only.
	return dedupeModels([]string{oc.effectiveModel(meta)})
}

func dedupeModels(models []string) []string {
	return stringutil.DedupeStrings(models)
}

// overrideModel returns a shallow copy of meta with a different model and refreshed capabilities.
func (oc *AIClient) overrideModel(meta *PortalMetadata, modelID string) *PortalMetadata {
	if meta == nil {
		return nil
	}
	metaCopy := *meta
	metaCopy.RuntimeModelOverride = ResolveAlias(modelID)
	return &metaCopy
}

type responseSelector func(meta *PortalMetadata, prompt []openai.ChatCompletionMessageParamUnion) (responseFunc, string)

func (oc *AIClient) responseWithModelFallbackDynamic(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
	selector responseSelector,
) {
	modelChain := oc.modelFallbackChain(ctx, meta)
	if len(modelChain) == 0 {
		modelChain = []string{oc.effectiveModel(meta)}
	}

	for idx, modelID := range modelChain {
		effectiveMeta := meta
		if meta != nil {
			effectiveMeta = oc.overrideModel(meta, modelID)
		}
		responseFn, logLabel := selector(effectiveMeta, prompt)
		success, err := oc.responseWithRetryAndReasoningFallback(ctx, evt, portal, effectiveMeta, prompt, responseFn, logLabel)
		if success {
			return
		}
		if err == nil {
			// Error already handled (context length or non-retryable path).
			return
		}
		var nf *NonFallbackError
		if errors.As(err, &nf) {
			oc.notifyMatrixSendFailure(ctx, portal, evt, err)
			return
		}
		decision := airuntime.DecideFallback(err)
		if decision.Action != airuntime.FallbackActionFailover || idx == len(modelChain)-1 {
			oc.notifyMatrixSendFailure(ctx, portal, evt, err)
			return
		}
		oc.loggerForContext(ctx).Warn().
			Err(err).
			Str("failed_model", modelID).
			Str("next_model", modelChain[idx+1]).
			Str("fallback_action", string(decision.Action)).
			Str("fallback_class", string(decision.Class)).
			Str("fallback_reason", decision.Reason).
			Str("failover_reason", string(ClassifyFailoverReason(err))).
			Msg("Model failed; falling back to next model")
	}
}
