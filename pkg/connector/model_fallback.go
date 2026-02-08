package connector

import (
	"context"
	"errors"
	"strings"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// shouldFallbackOnError determines if a model fallback should be attempted.
// Mirrors OpenClaw's fallback triggers (auth, rate limits, timeouts).
func shouldFallbackOnError(err error) bool {
	var nf *NonFallbackError
	if errors.As(err, &nf) {
		return false
	}
	return IsAuthError(err) ||
		IsRateLimitError(err) ||
		IsTimeoutError(err) ||
		IsOverloadedError(err) ||
		IsBillingError(err) ||
		IsModelNotFound(err) ||
		IsServerError(err)
}

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
// Room-level overrides take priority and disable fallbacks.
func (oc *AIClient) modelFallbackChain(ctx context.Context, meta *PortalMetadata) []string {
	// Explicit room-level model overrides should not fall back.
	if meta != nil && strings.TrimSpace(meta.Model) != "" {
		return dedupeModels([]string{ResolveAlias(meta.Model)})
	}

	agentID := ""
	if meta != nil {
		agentID = meta.AgentID
	}

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
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	return out
}

// overrideModel returns a shallow copy of meta with a different model and refreshed capabilities.
func (oc *AIClient) overrideModel(meta *PortalMetadata, modelID string) *PortalMetadata {
	if meta == nil {
		return nil
	}
	metaCopy := *meta
	metaCopy.Model = modelID
	metaCopy.Capabilities = getModelCapabilities(modelID, oc.findModelInfo(modelID))
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
		if !shouldFallbackOnError(err) || idx == len(modelChain)-1 {
			oc.notifyMatrixSendFailure(ctx, portal, evt, err)
			return
		}
		oc.loggerForContext(ctx).Warn().
			Err(err).
			Str("failed_model", modelID).
			Str("next_model", modelChain[idx+1]).
			Str("failover_reason", string(ClassifyFailoverReason(err))).
			Msg("Model failed; falling back to next model")
	}
}
