package ai

import "context"

// selectedBuiltinToolsForTurn returns builtin tools exposed to the model for a turn.
func (oc *AIClient) selectedBuiltinToolsForTurn(ctx context.Context, meta *PortalMetadata) []ToolDefinition {
	if meta == nil || !oc.getModelCapabilitiesForMeta(meta).SupportsToolCalling {
		return nil
	}

	if resolveAgentID(meta) == "" {
		return nil
	}

	return oc.enabledBuiltinToolsForModel(ctx, meta)
}
