package connector

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/agents"
)

func (oc *AIClient) applyModelDirective(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	rawModel string,
	persist bool,
) (ack string, changed bool, errText string) {
	if meta == nil {
		return "", false, "Model unavailable."
	}
	trimmed := strings.TrimSpace(rawModel)
	if trimmed == "" {
		return fmt.Sprintf("Current model: %s", oc.effectiveModel(meta)), false, ""
	}
	if agents.IsBossAgent(resolveAgentID(meta)) {
		return "", false, "Can't change the model in a room managed by the Boss agent."
	}
	if agentID := resolveAgentID(meta); agentID != "" {
		return "", false, "Can't set the room model while an agent is assigned. Edit the agent instead."
	}

	oldModel := meta.Model
	if strings.EqualFold(trimmed, "default") || strings.EqualFold(trimmed, "reset") {
		meta.Model = ""
		newModel := oc.effectiveModel(meta)
		meta.Capabilities = getModelCapabilities(newModel, oc.findModelInfo(newModel))
		if persist && portal != nil {
			oc.savePortalQuiet(ctx, portal, "model reset")
			if oldModel != "" && newModel != "" && newModel != oldModel {
				oc.handleModelSwitch(ctx, portal, oldModel, newModel)
			}
		}
		if oldModel != "" && newModel != oldModel {
			changed = true
		}
		return fmt.Sprintf("Model reset to %s.", newModel), changed, ""
	}

	valid, err := oc.validateModel(ctx, trimmed)
	if err != nil || !valid {
		return "", false, fmt.Sprintf("That model isn't available: %s", trimmed)
	}
	meta.Model = trimmed
	meta.Capabilities = getModelCapabilities(trimmed, oc.findModelInfo(trimmed))
	if persist && portal != nil {
		oc.savePortalQuiet(ctx, portal, "model change")
		oc.ensureGhostDisplayName(ctx, trimmed)
		if oldModel != "" && trimmed != oldModel {
			oc.handleModelSwitch(ctx, portal, oldModel, trimmed)
		}
	}
	if oldModel != "" && trimmed != oldModel {
		changed = true
	}
	return fmt.Sprintf("Model set to %s.", trimmed), changed, ""
}
