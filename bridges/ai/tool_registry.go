package ai

import (
	"context"

	agenttools "github.com/beeper/agentremote/pkg/agents/tools"
	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

type toolExecutor func(ctx context.Context, args map[string]any) (string, error)

func builtinToolExecutors() map[string]toolExecutor {
	return map[string]toolExecutor{
		ToolNameCalculator:         executeCalculator,
		ToolNameWebSearch:          executeWebSearchWithProviders,
		ToolNameMessage:            executeMessage,
		toolNameTTS:                executeTTS,
		toolNameWebFetch:           executeWebFetchWithProviders,
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
			Parameters:  jsonutil.ToMap(tool.InputSchema),
			Execute:     exec,
		})
	}
	return defs
}
