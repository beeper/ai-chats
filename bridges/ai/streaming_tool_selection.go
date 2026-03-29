package ai

import "context"

type builtinToolPreset string

const (
	builtinToolPresetNone  builtinToolPreset = ""
	builtinToolPresetModel builtinToolPreset = "model"
	builtinToolPresetAgent builtinToolPreset = "agent"
)

var modelChatBuiltinToolNames = []string{
	ToolNameWebSearch,
	toolNameWebFetch,
	toolNameSessionStatus,
}

func selectToolDefinitionsByName(available []ToolDefinition, names []string) []ToolDefinition {
	if len(available) == 0 || len(names) == 0 {
		return nil
	}

	availableByName := make(map[string]ToolDefinition, len(available))
	for _, tool := range available {
		if tool.Name == "" {
			continue
		}
		availableByName[tool.Name] = tool
	}

	selected := make([]ToolDefinition, 0, len(names))
	for _, name := range names {
		tool, ok := availableByName[name]
		if !ok {
			continue
		}
		selected = append(selected, tool)
	}
	return selected
}

func (oc *AIClient) builtinToolPresetForTurn(ctx context.Context, meta *PortalMetadata) builtinToolPreset {
	if meta == nil || !oc.getModelCapabilitiesForMeta(ctx, meta).SupportsToolCalling {
		return builtinToolPresetNone
	}
	if resolveAgentID(meta) == "" {
		return builtinToolPresetModel
	}
	return builtinToolPresetAgent
}

// selectedBuiltinToolsForTurn returns builtin tools exposed to the model for a turn.
func (oc *AIClient) selectedBuiltinToolsForTurn(ctx context.Context, meta *PortalMetadata) []ToolDefinition {
	preset := oc.builtinToolPresetForTurn(ctx, meta)
	if preset == builtinToolPresetNone {
		return nil
	}

	enabledTools := oc.enabledBuiltinToolsForModel(ctx, meta)
	switch preset {
	case builtinToolPresetModel:
		return selectToolDefinitionsByName(enabledTools, modelChatBuiltinToolNames)
	case builtinToolPresetAgent:
		return enabledTools
	default:
		return nil
	}
}
