package connector

import "context"

// selectedBuiltinToolsForTurn returns builtin tools exposed to the model for a turn.
// Simple mode stays minimal: it only exposes web_search when tool-calling is supported.
func (oc *AIClient) selectedBuiltinToolsForTurn(ctx context.Context, meta *PortalMetadata) []ToolDefinition {
	if meta == nil || !oc.getModelCapabilitiesForMeta(meta).SupportsToolCalling {
		return nil
	}

	if isSimpleMode(meta) {
		if !oc.isToolEnabled(meta, ToolNameWebSearch) {
			return nil
		}
		for _, tool := range oc.enabledBuiltinToolsForModel(ctx, meta) {
			if normalizeToolAlias(tool.Name) == ToolNameWebSearch {
				return []ToolDefinition{tool}
			}
		}
		return nil
	}

	if resolveAgentID(meta) == "" {
		return nil
	}

	return oc.enabledBuiltinToolsForModel(ctx, meta)
}
