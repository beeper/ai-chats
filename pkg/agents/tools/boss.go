package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/beeper/ai-bridge/pkg/agents/toolpolicy"
)

// Boss tools for agent management.
// These are executed via the executor when the Boss agent is active.

// BossToolExecutor handles boss tool execution with access to the agent store.
type BossToolExecutor struct {
	store AgentStoreInterface
}

// AgentStoreInterface is the interface that the boss tools need.
// This matches the AgentStore interface in the agents package but avoids import cycle.
type AgentStoreInterface interface {
	LoadAgents(ctx context.Context) (map[string]AgentData, error)
	SaveAgent(ctx context.Context, agent AgentData) error
	DeleteAgent(ctx context.Context, agentID string) error
	ListModels(ctx context.Context) ([]ModelData, error)
	ListAvailableTools(ctx context.Context) ([]ToolInfo, error)
	RunInternalCommand(ctx context.Context, roomID string, command string) (string, error)
	// Room management
	CreateRoom(ctx context.Context, room RoomData) (string, error)
	ModifyRoom(ctx context.Context, roomID string, updates RoomData) error
	ListRooms(ctx context.Context) ([]RoomData, error)
}

// AgentData represents agent data for boss tools (avoids import cycle).
type AgentData struct {
	ID           string                       `json:"id"`
	Name         string                       `json:"name"`
	Description  string                       `json:"description,omitempty"`
	Model        string                       `json:"model,omitempty"`
	SystemPrompt string                       `json:"system_prompt,omitempty"`
	Tools        *toolpolicy.ToolPolicyConfig `json:"tools,omitempty"`
	Subagents    *SubagentConfig              `json:"subagents,omitempty"`
	Temperature  float64                      `json:"temperature,omitempty"`
	IsPreset     bool                         `json:"is_preset,omitempty"`
	CreatedAt    int64                        `json:"created_at"`
	UpdatedAt    int64                        `json:"updated_at"`
}

// ModelData represents model data for boss tools.
type ModelData struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider,omitempty"`
	Description string `json:"description,omitempty"`
}

// RoomData represents room data for boss tools.
type RoomData struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	AgentID      string `json:"agent_id,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

// NewBossToolExecutor creates a new boss tool executor.
func NewBossToolExecutor(store AgentStoreInterface) *BossToolExecutor {
	return &BossToolExecutor{store: store}
}

// CreateAgent tool definition.
var CreateAgentTool = &Tool{
	Tool: mcp.Tool{
		Name:        "create_agent",
		Description: "Create a new AI agent with custom configuration",
		Annotations: &mcp.ToolAnnotations{Title: "Create Agent"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Display name for the agent",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Brief description of what the agent does",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Model ID to use (e.g., 'anthropic/claude-sonnet-4.5'). Leave empty for default.",
				},
				"system_prompt": map[string]any{
					"type":        "string",
					"description": "Custom system prompt for the agent",
				},
				"tools": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"profile": map[string]any{
							"type":        "string",
							"enum":        []string{"minimal", "coding", "messaging", "full", "boss"},
							"description": "Tool access profile (OpenClaw-style)",
						},
						"allow": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Explicit tool allowlist (supports wildcards like 'web_*' or group:... shorthands)",
						},
						"alsoAllow": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Additional allowlist entries merged into allow",
						},
						"deny": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Explicit tool denylist (deny wins)",
						},
						"byProvider": map[string]any{
							"type":                 "object",
							"additionalProperties": map[string]any{"type": "object"},
							"description":          "Optional provider- or model-specific overrides keyed by provider or provider/model",
						},
					},
				},
				"subagents": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"model": map[string]any{
							"type":        "string",
							"description": "Default model override for subagents spawned by this agent",
						},
						"allowAgents": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Agent ids allowed for sessions_spawn (use \"*\" for any)",
						},
					},
				},
			},
			"required": []string{"name"},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupBuilder,
}

// ForkAgentTool tool definition.
var ForkAgentTool = &Tool{
	Tool: mcp.Tool{
		Name:        "fork_agent",
		Description: "Create a copy of an existing agent as a new custom agent",
		Annotations: &mcp.ToolAnnotations{Title: "Fork Agent"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source_id": map[string]any{
					"type":        "string",
					"description": "ID of the agent to copy",
				},
				"new_name": map[string]any{
					"type":        "string",
					"description": "Name for the new agent (defaults to '[Original Name] (Fork)')",
				},
			},
			"required": []string{"source_id"},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupBuilder,
}

// EditAgentTool tool definition.
var EditAgentTool = &Tool{
	Tool: mcp.Tool{
		Name:        "edit_agent",
		Description: "Modify an existing custom agent's configuration",
		Annotations: &mcp.ToolAnnotations{Title: "Edit Agent"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "ID of the agent to edit",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "New display name",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "New description",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "New model ID",
				},
				"system_prompt": map[string]any{
					"type":        "string",
					"description": "New system prompt",
				},
				"tools": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"profile": map[string]any{
							"type":        "string",
							"enum":        []string{"minimal", "coding", "messaging", "full", "boss"},
							"description": "Tool access profile (OpenClaw-style)",
						},
						"allow": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Explicit tool allowlist (supports wildcards like 'web_*' or group:... shorthands)",
						},
						"alsoAllow": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Additional allowlist entries merged into allow",
						},
						"deny": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Explicit tool denylist (deny wins)",
						},
						"byProvider": map[string]any{
							"type":                 "object",
							"additionalProperties": map[string]any{"type": "object"},
							"description":          "Optional provider- or model-specific overrides keyed by provider or provider/model",
						},
					},
				},
				"subagents": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"model": map[string]any{
							"type":        "string",
							"description": "Default model override for subagents spawned by this agent",
						},
						"allowAgents": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Agent ids allowed for sessions_spawn (use \"*\" for any)",
						},
					},
				},
			},
			"required": []string{"agent_id"},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupBuilder,
}

// DeleteAgentTool tool definition.
var DeleteAgentTool = &Tool{
	Tool: mcp.Tool{
		Name:        "delete_agent",
		Description: "Delete a custom agent (preset agents cannot be deleted)",
		Annotations: &mcp.ToolAnnotations{Title: "Delete Agent"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "ID of the agent to delete",
				},
			},
			"required": []string{"agent_id"},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupBuilder,
}

// ListAgentsTool tool definition.
var ListAgentsTool = &Tool{
	Tool: mcp.Tool{
		Name:        "list_agents",
		Description: "List all available agents (both preset and custom)",
		Annotations: &mcp.ToolAnnotations{Title: "List Agents"},
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupBuilder,
}

// ListModelsTool tool definition.
var ListModelsTool = &Tool{
	Tool: mcp.Tool{
		Name:        "list_models",
		Description: "List all available AI models",
		Annotations: &mcp.ToolAnnotations{Title: "List Models"},
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupSessions,
}

// ListToolsDef tool definition.
var ListToolsDef = &Tool{
	Tool: mcp.Tool{
		Name:        "list_tools",
		Description: "List all available tools and their profiles",
		Annotations: &mcp.ToolAnnotations{Title: "List Tools"},
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupSessions,
}

// RunInternalCommandTool tool definition.
var RunInternalCommandTool = &Tool{
	Tool: mcp.Tool{
		Name:        "run_internal_command",
		Description: "Run an internal !ai command in a target room",
		Annotations: &mcp.ToolAnnotations{Title: "Run Internal Command"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The !ai command to run (with or without prefix)",
				},
				"room_id": map[string]any{
					"type":        "string",
					"description": "Optional target room ID (defaults to the current room)",
				},
			},
			"required": []string{"command"},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupBuilder,
}

// ModifyRoomTool tool definition.
var ModifyRoomTool = &Tool{
	Tool: mcp.Tool{
		Name:        "modify_room",
		Description: "Modify an existing room's configuration",
		Annotations: &mcp.ToolAnnotations{Title: "Modify Room"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"room_id": map[string]any{
					"type":        "string",
					"description": "ID of the room to modify",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "New display name for the room",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "New agent ID to assign to this room",
				},
				"system_prompt": map[string]any{
					"type":        "string",
					"description": "New system prompt override for this room",
				},
			},
			"required": []string{"room_id"},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupSessions,
}

// SessionsListTool tool definition.
var SessionsListTool = &Tool{
	Tool: mcp.Tool{
		Name:        "sessions_list",
		Description: "List sessions with optional filters and last messages.",
		Annotations: &mcp.ToolAnnotations{Title: "List Sessions"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kinds": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"limit": map[string]any{
					"type":        "number",
					"description": "Maximum number of sessions to return (default: 50)",
				},
				"activeMinutes": map[string]any{
					"type":        "number",
					"description": "Only include sessions active within this many minutes",
				},
				"messageLimit": map[string]any{
					"type":        "number",
					"description": "Include the last N messages for each session",
				},
			},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupSessions,
}

// SessionsHistoryTool tool definition.
var SessionsHistoryTool = &Tool{
	Tool: mcp.Tool{
		Name:        "sessions_history",
		Description: "Fetch message history for a session. Use the sessionKey from sessions_list.",
		Annotations: &mcp.ToolAnnotations{Title: "Session History"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sessionKey": map[string]any{
					"type":        "string",
					"description": "Session identifier from sessions_list (preferred canonical target)",
				},
				"limit": map[string]any{
					"type":        "number",
					"description": "Maximum number of messages to return (default: 200)",
				},
				"includeTools": map[string]any{
					"type":        "boolean",
					"description": "Whether to include tool calls in the returned history",
				},
			},
			"required": []string{"sessionKey"},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupSessions,
}

// SessionsSendTool tool definition.
var SessionsSendTool = &Tool{
	Tool: mcp.Tool{
		Name:        "sessions_send",
		Description: "Send a message into another session. Prefer the sessionKey from sessions_list; label is fallback only.",
		Annotations: &mcp.ToolAnnotations{Title: "Send to Session"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sessionKey": map[string]any{
					"type":        "string",
					"description": "Session identifier from sessions_list (preferred canonical target)",
				},
				"label": map[string]any{
					"type":        "string",
					"description": "Session label fallback (can be ambiguous; sessionKey is preferred)",
				},
				"agentId": map[string]any{
					"type":        "string",
					"description": "Agent id filter for label lookups",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "The message to send",
				},
				"timeoutSeconds": map[string]any{
					"type":        "number",
					"description": "Optional timeout for the remote session",
				},
			},
			"required": []string{"message"},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupSessions,
}

// SessionsSpawnTool tool definition.
var SessionsSpawnTool = &Tool{
	Tool: mcp.Tool{
		Name:        "sessions_spawn",
		Description: "Spawn a background sub-agent run in an isolated session and announce the result back to the requester chat.",
		Annotations: &mcp.ToolAnnotations{Title: "Spawn Session"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "Task description for the sub-agent.",
				},
				"label": map[string]any{
					"type":        "string",
					"description": "Optional label for the sub-agent run.",
				},
				"agentId": map[string]any{
					"type":        "string",
					"description": "Agent ID override for the sub-agent run.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Optional model override (provider/model).",
				},
				"thinking": map[string]any{
					"type":        "string",
					"description": "Optional thinking level override.",
				},
				"runTimeoutSeconds": map[string]any{
					"type":        "number",
					"description": "Optional run timeout in seconds.",
				},
				"timeoutSeconds": map[string]any{
					"type":        "number",
					"description": "Legacy alias for runTimeoutSeconds.",
				},
				"cleanup": map[string]any{
					"type":        "string",
					"enum":        []string{"delete", "keep"},
					"description": "Cleanup policy for the spawned session.",
				},
			},
			"required": []string{"task"},
		},
	},
	Type:  ToolTypeBuiltin,
	Group: GroupSessions,
}

// BossTools returns all boss agent tools.
func BossTools() []*Tool {
	return []*Tool{
		// Agent management
		CreateAgentTool,
		ForkAgentTool,
		EditAgentTool,
		DeleteAgentTool,
		ListAgentsTool,
		RunInternalCommandTool,
	}
}

// SessionTools returns the cross-session tools available to non-boss agents.
func SessionTools() []*Tool {
	return []*Tool{
		AgentsListTool,
		ListModelsTool,
		ListToolsDef,
		ModifyRoomTool,
		SessionsListTool,
		SessionsHistoryTool,
		SessionsSendTool,
		SessionsSpawnTool,
	}
}

// IsSessionTool checks if a tool name is a session tool.
func IsSessionTool(toolName string) bool {
	for _, t := range SessionTools() {
		if t.Name == toolName {
			return true
		}
	}
	return false
}

// IsBossTool checks if a tool name is a Boss agent tool.
func IsBossTool(toolName string) bool {
	for _, t := range BossTools() {
		if t.Name == toolName {
			return true
		}
	}
	return false
}

func readToolPolicyConfig(input map[string]any) (*toolpolicy.ToolPolicyConfig, error) {
	raw, err := ReadMap(input, "tools", false)
	if err != nil || raw == nil {
		return nil, err
	}
	bytes, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var cfg toolpolicy.ToolPolicyConfig
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func readSubagentConfig(input map[string]any) (*SubagentConfig, error) {
	raw, err := ReadMap(input, "subagents", false)
	if err != nil || raw == nil {
		return nil, err
	}
	bytes, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var cfg SubagentConfig
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return nil, err
	}
	if cfg.Model == "" && len(cfg.AllowAgents) == 0 {
		return nil, nil
	}
	return &cfg, nil
}

// loadAgent loads all agents and looks up the one with the given ID.
// On failure it returns an ErrorResult that the caller can return directly.
func (e *BossToolExecutor) loadAgent(ctx context.Context, toolName, agentID string) (AgentData, *Result, error) {
	agents, err := e.store.LoadAgents(ctx)
	if err != nil {
		return AgentData{}, ErrorResult(toolName, fmt.Sprintf("failed to load agents: %v", err)), nil
	}
	agent, ok := agents[agentID]
	if !ok {
		return AgentData{}, ErrorResult(toolName, fmt.Sprintf("agent '%s' not found", agentID)), nil
	}
	return agent, nil, nil
}

// ExecuteCreateAgent handles the create_agent tool.
func (e *BossToolExecutor) ExecuteCreateAgent(ctx context.Context, input map[string]any) (*Result, error) {
	name, err := ReadString(input, "name", true)
	if err != nil {
		return ErrorResult("create_agent", err.Error()), nil
	}

	description := ReadStringDefault(input, "description", "")
	model := ReadStringDefault(input, "model", "")
	systemPrompt := ReadStringDefault(input, "system_prompt", "")
	toolsConfig, err := readToolPolicyConfig(input)
	if err != nil {
		return ErrorResult("create_agent", fmt.Sprintf("invalid tools config: %v", err)), nil
	}
	subagentsConfig, err := readSubagentConfig(input)
	if err != nil {
		return ErrorResult("create_agent", fmt.Sprintf("invalid subagents config: %v", err)), nil
	}
	if toolsConfig == nil {
		toolsConfig = &toolpolicy.ToolPolicyConfig{Profile: toolpolicy.ProfileFull}
	}

	agentID := uuid.NewString()

	now := time.Now().Unix()

	agent := AgentData{
		ID:           agentID,
		Name:         name,
		Description:  description,
		Model:        model,
		SystemPrompt: systemPrompt,
		Tools:        toolsConfig,
		Subagents:    subagentsConfig,
		IsPreset:     false,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := e.store.SaveAgent(ctx, agent); err != nil {
		return ErrorResult("create_agent", fmt.Sprintf("failed to save agent: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"success":  true,
		"agent_id": agentID,
		"message":  fmt.Sprintf("Created agent '%s' with ID '%s'", name, agentID),
	}), nil
}

// ExecuteForkAgent handles the fork_agent tool.
func (e *BossToolExecutor) ExecuteForkAgent(ctx context.Context, input map[string]any) (*Result, error) {
	sourceID, err := ReadString(input, "source_id", true)
	if err != nil {
		return ErrorResult("fork_agent", err.Error()), nil
	}

	source, errResult, err := e.loadAgent(ctx, "fork_agent", sourceID)
	if errResult != nil || err != nil {
		return errResult, err
	}

	newName := ReadStringDefault(input, "new_name", fmt.Sprintf("%s (Fork)", source.Name))

	agentID := uuid.NewString()

	now := time.Now().Unix()

	forked := AgentData{
		ID:           agentID,
		Name:         newName,
		Description:  source.Description,
		Model:        source.Model,
		SystemPrompt: source.SystemPrompt,
		Tools:        source.Tools.Clone(),
		Subagents:    cloneSubagentConfig(source.Subagents),
		Temperature:  source.Temperature,
		IsPreset:     false,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := e.store.SaveAgent(ctx, forked); err != nil {
		return ErrorResult("fork_agent", fmt.Sprintf("failed to save forked agent: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"success":   true,
		"agent_id":  agentID,
		"source_id": sourceID,
		"message":   fmt.Sprintf("Forked '%s' as '%s' with ID '%s'", source.Name, newName, agentID),
	}), nil
}

// ExecuteEditAgent handles the edit_agent tool.
func (e *BossToolExecutor) ExecuteEditAgent(ctx context.Context, input map[string]any) (*Result, error) {
	agentID, err := ReadString(input, "agent_id", true)
	if err != nil {
		return ErrorResult("edit_agent", err.Error()), nil
	}

	agent, errResult, err := e.loadAgent(ctx, "edit_agent", agentID)
	if errResult != nil || err != nil {
		return errResult, err
	}

	if agent.IsPreset {
		return ErrorResult("edit_agent", "cannot modify preset agents - fork it first"), nil
	}

	// Apply updates
	if name, _ := ReadString(input, "name", false); name != "" {
		agent.Name = name
	}
	if desc, _ := ReadString(input, "description", false); desc != "" {
		agent.Description = desc
	}
	if model, _ := ReadString(input, "model", false); model != "" {
		agent.Model = model
	}
	if prompt, _ := ReadString(input, "system_prompt", false); prompt != "" {
		agent.SystemPrompt = prompt
	}
	if toolsConfig, err := readToolPolicyConfig(input); err == nil && toolsConfig != nil {
		agent.Tools = toolsConfig
	} else if err != nil {
		return ErrorResult("edit_agent", fmt.Sprintf("invalid tools config: %v", err)), nil
	}
	if subagentsConfig, err := readSubagentConfig(input); err == nil && subagentsConfig != nil {
		agent.Subagents = subagentsConfig
	} else if err != nil {
		return ErrorResult("edit_agent", fmt.Sprintf("invalid subagents config: %v", err)), nil
	}

	agent.UpdatedAt = time.Now().Unix()

	if err := e.store.SaveAgent(ctx, agent); err != nil {
		return ErrorResult("edit_agent", fmt.Sprintf("failed to save agent: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"success":  true,
		"agent_id": agentID,
		"message":  fmt.Sprintf("Updated agent '%s'", agent.Name),
	}), nil
}

// ExecuteDeleteAgent handles the delete_agent tool.
func (e *BossToolExecutor) ExecuteDeleteAgent(ctx context.Context, input map[string]any) (*Result, error) {
	agentID, err := ReadString(input, "agent_id", true)
	if err != nil {
		return ErrorResult("delete_agent", err.Error()), nil
	}

	agent, errResult, err := e.loadAgent(ctx, "delete_agent", agentID)
	if errResult != nil || err != nil {
		return errResult, err
	}

	if agent.IsPreset {
		return ErrorResult("delete_agent", "cannot delete preset agents"), nil
	}

	if err := e.store.DeleteAgent(ctx, agentID); err != nil {
		return ErrorResult("delete_agent", fmt.Sprintf("failed to delete agent: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"success":  true,
		"agent_id": agentID,
		"message":  fmt.Sprintf("Deleted agent '%s'", agent.Name),
	}), nil
}

// ExecuteListAgents handles the list_agents tool.
func (e *BossToolExecutor) ExecuteListAgents(ctx context.Context, _ map[string]any) (*Result, error) {
	agents, err := e.store.LoadAgents(ctx)
	if err != nil {
		return ErrorResult("list_agents", fmt.Sprintf("failed to load agents: %v", err)), nil
	}

	var agentList []map[string]any
	for _, agent := range agents {
		agentList = append(agentList, map[string]any{
			"id":          agent.ID,
			"name":        agent.Name,
			"description": agent.Description,
			"model":       agent.Model,
			"tools":       agent.Tools,
			"is_preset":   agent.IsPreset,
		})
	}

	return JSONResult(map[string]any{
		"agents": agentList,
		"count":  len(agentList),
	}), nil
}

// ExecuteListModels handles the list_models tool.
func (e *BossToolExecutor) ExecuteListModels(ctx context.Context, _ map[string]any) (*Result, error) {
	models, err := e.store.ListModels(ctx)
	if err != nil {
		return ErrorResult("list_models", fmt.Sprintf("failed to load models: %v", err)), nil
	}

	var modelList []map[string]any
	for _, model := range models {
		modelList = append(modelList, map[string]any{
			"id":          model.ID,
			"name":        model.Name,
			"provider":    model.Provider,
			"description": model.Description,
		})
	}

	return JSONResult(map[string]any{
		"models": modelList,
		"count":  len(modelList),
	}), nil
}

// ExecuteListTools handles the list_tools tool.
func (e *BossToolExecutor) ExecuteListTools(ctx context.Context, _ map[string]any) (*Result, error) {
	toolInfos, err := e.store.ListAvailableTools(ctx)
	if err != nil {
		return ErrorResult("list_tools", fmt.Sprintf("failed to load tools: %v", err)), nil
	}

	var toolList []map[string]any
	for _, tool := range toolInfos {
		toolList = append(toolList, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"type":        string(tool.Type),
			"group":       tool.Group,
			"enabled":     tool.Enabled,
		})
	}

	// Add profile descriptions
	profiles := map[string][]string{}
	for profile, policy := range toolpolicy.ToolProfiles {
		if len(policy.Allow) == 0 {
			continue
		}
		profiles[string(profile)] = append([]string{}, policy.Allow...)
	}

	return JSONResult(map[string]any{
		"tools":    toolList,
		"count":    len(toolList),
		"profiles": profiles,
	}), nil
}

// ExecuteRunInternalCommand handles the run_internal_command tool.
func (e *BossToolExecutor) ExecuteRunInternalCommand(ctx context.Context, input map[string]any) (*Result, error) {
	command, err := ReadString(input, "command", true)
	if err != nil {
		return ErrorResult("run_internal_command", err.Error()), nil
	}

	roomID := ReadStringDefault(input, "room_id", "")
	if roomID == "" {
		return ErrorResult("run_internal_command", "room_id is required"), nil
	}

	message, err := e.store.RunInternalCommand(ctx, roomID, command)
	if err != nil {
		return ErrorResult("run_internal_command", fmt.Sprintf("failed to run command: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"success": true,
		"room_id": roomID,
		"command": command,
		"message": message,
	}), nil
}

// ExecuteModifyRoom handles the modify_room tool.
func (e *BossToolExecutor) ExecuteModifyRoom(ctx context.Context, input map[string]any) (*Result, error) {
	roomID, err := ReadString(input, "room_id", true)
	if err != nil {
		return ErrorResult("modify_room", err.Error()), nil
	}

	updates := RoomData{}

	if name, _ := ReadString(input, "name", false); name != "" {
		updates.Name = name
	}
	if agentID, _ := ReadString(input, "agent_id", false); agentID != "" {
		updates.AgentID = agentID
	}
	if prompt, _ := ReadString(input, "system_prompt", false); prompt != "" {
		updates.SystemPrompt = prompt
	}

	if err := e.store.ModifyRoom(ctx, roomID, updates); err != nil {
		return ErrorResult("modify_room", fmt.Sprintf("failed to modify room: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"success": true,
		"room_id": roomID,
		"message": fmt.Sprintf("Modified room '%s'", roomID),
	}), nil
}
