package connector

import (
	"context"
	"encoding/json"

	agenttools "github.com/beeper/ai-bridge/pkg/agents/tools"
)

type toolExecutor func(ctx context.Context, args map[string]any) (string, error)

func builtinToolExecutors() map[string]toolExecutor {
	return map[string]toolExecutor{
		ToolNameCalculator:         executeCalculator,
		ToolNameWebSearch:          executeWebSearch,
		ToolNameMessage:            executeMessage,
		toolNameTTS:                executeTTS,
		toolNameWebFetch:           executeWebFetch,
		ToolNameImage:              executeAnalyzeImage,
		ToolNameImageGenerate:      executeImageGeneration,
		toolNameSessionStatus:      executeSessionStatus,
		ToolNameRead:               executeReadFile,
		ToolNameApplyPatch:         executeApplyPatch,
		ToolNameWrite:              executeWriteFile,
		ToolNameEdit:               executeEditFile,
		ToolNameGravatarFetch:      executeGravatarFetch,
		ToolNameGravatarSet:        executeGravatarSet,
		ToolNameBeeperDocs:         executeBeeperDocs,
		ToolNameBeeperSendFeedback: executeBeeperSendFeedback,
	}
}

func buildBuiltinToolDefinitions() []ToolDefinition {
	executors := builtinToolExecutors()
	builtin := agenttools.BuiltinTools()
	defs := make([]ToolDefinition, 0, len(builtin))
	for _, tool := range builtin {
		if tool == nil || tool.Name == "" {
			continue
		}
		exec := executors[tool.Name]
		if exec == nil {
			continue // Module-owned tool, skip from builtin set
		}
		defs = append(defs, ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  toolSchemaToMap(tool.InputSchema),
			Execute:     exec,
		})
	}
	return defs
}

func toolSchemaToMap(schema any) map[string]any {
	switch v := schema.(type) {
	case nil:
		return nil
	case map[string]any:
		return v
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		var out map[string]any
		if err := json.Unmarshal(encoded, &out); err != nil {
			return nil
		}
		return out
	}
}
