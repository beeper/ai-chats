package connector

import (
	"context"
	"encoding/json"
	"fmt"

	agenttools "github.com/beeper/ai-bridge/pkg/agents/tools"
	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

type toolExecutor func(ctx context.Context, args map[string]any) (string, error)

func builtinToolExecutors() map[string]toolExecutor {
	return map[string]toolExecutor{
		toolspec.CalculatorName:                executeCalculator,
		toolspec.WebSearchName:                 executeWebSearch,
		ToolNameMessage:                        executeMessage,
		ToolNameTTS:                            executeTTS,
		ToolNameWebFetch:                       executeWebFetch,
		ToolNameCron:                           executeCron,
		ToolNameImage:                          executeAnalyzeImage,
		ToolNameImageGenerate:                  executeImageGeneration,
		ToolNameSessionStatus:                  executeSessionStatus,
		ToolNameMemorySearch:                   executeMemorySearch,
		ToolNameMemoryGet:                      executeMemoryGet,
		ToolNameRead:                           executeReadFile,
		ToolNameApplyPatch:                     executeApplyPatch,
		ToolNameWrite:                          executeWriteFile,
		ToolNameEdit:                           executeEditFile,
		ToolNameGravatarFetch:                  executeGravatarFetch,
		ToolNameGravatarSet:                    executeGravatarSet,
		ToolNameBeeperDocs:                     executeBeeperDocs,
		toolspec.NexusContactsName:             executeNexusContacts,
		toolspec.NexusSearchContactsName:       makeNexusExecutor(nexusToolRoutes[toolspec.NexusSearchContactsName]),
		toolspec.NexusGetContactName:           makeNexusExecutor(nexusToolRoutes[toolspec.NexusGetContactName]),
		toolspec.NexusCreateContactName:        makeNexusExecutor(nexusToolRoutes[toolspec.NexusCreateContactName]),
		toolspec.NexusUpdateContactName:        makeNexusExecutor(nexusToolRoutes[toolspec.NexusUpdateContactName]),
		toolspec.NexusArchiveContactName:       makeNexusExecutor(nexusToolRoutes[toolspec.NexusArchiveContactName]),
		toolspec.NexusRestoreContactName:       makeNexusExecutor(nexusToolRoutes[toolspec.NexusRestoreContactName]),
		toolspec.NexusCreateNoteName:           makeNexusExecutor(nexusToolRoutes[toolspec.NexusCreateNoteName]),
		toolspec.NexusGetGroupsName:            makeNexusExecutor(nexusToolRoutes[toolspec.NexusGetGroupsName]),
		toolspec.NexusCreateGroupName:          makeNexusExecutor(nexusToolRoutes[toolspec.NexusCreateGroupName]),
		toolspec.NexusUpdateGroupName:          makeNexusExecutor(nexusToolRoutes[toolspec.NexusUpdateGroupName]),
		toolspec.NexusGetNotesName:             makeNexusExecutor(nexusToolRoutes[toolspec.NexusGetNotesName]),
		toolspec.NexusGetEventsName:            makeNexusExecutor(nexusToolRoutes[toolspec.NexusGetEventsName]),
		toolspec.NexusGetUpcomingEventsName:    makeNexusExecutor(nexusToolRoutes[toolspec.NexusGetUpcomingEventsName]),
		toolspec.NexusGetEmailsName:            makeNexusExecutor(nexusToolRoutes[toolspec.NexusGetEmailsName]),
		toolspec.NexusGetRecentEmailsName:      makeNexusExecutor(nexusToolRoutes[toolspec.NexusGetRecentEmailsName]),
		toolspec.NexusGetRecentRemindersName:   makeNexusExecutor(nexusToolRoutes[toolspec.NexusGetRecentRemindersName]),
		toolspec.NexusGetUpcomingRemindersName: makeNexusExecutor(nexusToolRoutes[toolspec.NexusGetUpcomingRemindersName]),
		toolspec.NexusFindDuplicatesName:       makeNexusExecutor(nexusToolRoutes[toolspec.NexusFindDuplicatesName]),
		toolspec.NexusMergeContactsName:        makeNexusExecutor(nexusToolRoutes[toolspec.NexusMergeContactsName]),
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
			panic(fmt.Sprintf("missing executor for builtin tool %q", tool.Name))
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
