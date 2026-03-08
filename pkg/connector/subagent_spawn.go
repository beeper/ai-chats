package connector

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/tools"
	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

func normalizeAgentID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (oc *AIClient) resolveSubagentAllowlist(ctx context.Context, requesterAgentID string) (bool, map[string]struct{}) {
	allowSet := make(map[string]struct{})
	allowAny := false

	var allowList []string
	if requesterAgentID != "" {
		store := NewAgentStoreAdapter(oc)
		if agent, _ := store.GetAgentByID(ctx, requesterAgentID); agent != nil && agent.Subagents != nil {
			allowList = agent.Subagents.AllowAgents
		}
	}
	if len(allowList) == 0 && oc.connector != nil && oc.connector.Config.Agents != nil &&
		oc.connector.Config.Agents.Defaults != nil && oc.connector.Config.Agents.Defaults.Subagents != nil {
		allowList = oc.connector.Config.Agents.Defaults.Subagents.AllowAgents
	}

	for _, entry := range allowList {
		normalized := normalizeAgentID(entry)
		if normalized == "" {
			continue
		}
		if normalized == "*" {
			allowAny = true
			continue
		}
		allowSet[normalized] = struct{}{}
	}

	return allowAny, allowSet
}

func subagentModel(agent *agents.AgentDefinition, defaults *agents.SubagentConfig) string {
	if agent != nil && agent.Subagents != nil && agent.Subagents.Model != "" {
		return agent.Subagents.Model
	}
	if defaults != nil && defaults.Model != "" {
		return defaults.Model
	}
	return ""
}

func subagentThinking(agent *agents.AgentDefinition, defaults *agents.SubagentConfig) string {
	if agent != nil && agent.Subagents != nil && agent.Subagents.Thinking != "" {
		return agent.Subagents.Thinking
	}
	if defaults != nil && defaults.Thinking != "" {
		return defaults.Thinking
	}
	return ""
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeThinkingLevel(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return "", true
	}
	key := strings.ToLower(strings.TrimSpace(raw))
	switch key {
	case "off":
		return "off", true
	case "on", "enable", "enabled":
		return "low", true
	case "min", "minimal":
		return "minimal", true
	case "low", "thinkhard", "think-hard", "think_hard":
		return "low", true
	case "mid", "med", "medium", "thinkharder", "think-harder", "harder":
		return "medium", true
	case "high", "ultra", "ultrathink", "thinkhardest", "highest", "max":
		return "high", true
	case "xhigh", "x-high", "x_high":
		return "xhigh", true
	case "think":
		return "minimal", true
	default:
		return "", false
	}
}

func mapThinkingToReasoningEffort(level string) string {
	switch level {
	case "off", "":
		return ""
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high", "xhigh":
		return "high"
	default:
		return ""
	}
}

func resolveRunTimeoutSeconds(args map[string]any) time.Duration {
	read := func(key string) (int, bool) {
		raw, ok := args[key]
		if !ok || raw == nil {
			return 0, false
		}
		switch v := raw.(type) {
		case float64:
			if v < 0 {
				return 0, false
			}
			return int(v), true
		case int:
			if v < 0 {
				return 0, false
			}
			return v, true
		case int64:
			if v < 0 {
				return 0, false
			}
			return int(v), true
		}
		return 0, false
	}

	if seconds, ok := read("runTimeoutSeconds"); ok && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return 0
}

func resolveSubagentRoomName(label, task string) string {
	if trimmed := strings.TrimSpace(label); trimmed != "" {
		return trimmed
	}
	trimmedTask := strings.TrimSpace(task)
	if trimmedTask == "" {
		return ""
	}
	trimmedTask = strings.Join(strings.Fields(trimmedTask), " ")
	const maxLen = 64
	if len(trimmedTask) > maxLen {
		trimmedTask = trimmedTask[:maxLen]
	}
	return fmt.Sprintf("Subagent: %s", trimmedTask)
}

func (oc *AIClient) executeSessionsSpawn(ctx context.Context, portal *bridgev2.Portal, args map[string]any) (*tools.Result, error) {
	task, err := tools.ReadString(args, "task", true)
	if err != nil || task == "" {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  "task is required",
		}), nil
	}
	label := tools.ReadStringDefault(args, "label", "")
	requestedAgentID := tools.ReadStringDefault(args, "agentId", "")
	modelOverride := tools.ReadStringDefault(args, "model", "")
	thinkingOverride := tools.ReadStringDefault(args, "thinking", "")
	cleanup := strings.ToLower(strings.TrimSpace(tools.ReadStringDefault(args, "cleanup", "keep")))
	if cleanup != "delete" {
		cleanup = "keep"
	}
	runTimeout := resolveRunTimeoutSeconds(args)

	if portal == nil || portal.MXID == "" {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  "missing room context",
		}), nil
	}

	meta := portalMeta(portal)
	if meta != nil && strings.TrimSpace(meta.SubagentParentRoomID) != "" {
		return tools.JSONResult(map[string]any{
			"status": "forbidden",
			"error":  "sessions_spawn is not allowed from sub-agent sessions",
		}), nil
	}

	requesterAgentID := normalizeAgentID(resolveAgentID(meta))
	if requesterAgentID == "" {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  "no agent assigned to this room",
		}), nil
	}

	targetAgentID := requesterAgentID
	if strings.TrimSpace(requestedAgentID) != "" {
		targetAgentID = normalizeAgentID(requestedAgentID)
	}

	allowAny, allowSet := oc.resolveSubagentAllowlist(ctx, requesterAgentID)
	if targetAgentID != requesterAgentID && !allowAny {
		_, allowed := allowSet[targetAgentID]
		if !allowed {
			allowedText := "none"
			if len(allowSet) > 0 {
				ids := slices.Sorted(maps.Keys(allowSet))
				allowedText = strings.Join(ids, ", ")
			}
			return tools.JSONResult(map[string]any{
				"status": "forbidden",
				"error":  fmt.Sprintf("agentId is not allowed for sessions_spawn (allowed: %s)", allowedText),
			}), nil
		}
	}

	store := NewAgentStoreAdapter(oc)
	targetAgent, err := store.GetAgentByID(ctx, targetAgentID)
	if err != nil || targetAgent == nil {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  fmt.Sprintf("agentId not found: %s", targetAgentID),
		}), nil
	}

	defaultSubagents := (*agents.SubagentConfig)(nil)
	if oc.connector != nil && oc.connector.Config.Agents != nil && oc.connector.Config.Agents.Defaults != nil {
		defaultSubagents = oc.connector.Config.Agents.Defaults.Subagents
	}
	thinkingCandidate := firstNonEmptyTrimmed(thinkingOverride, subagentThinking(targetAgent, defaultSubagents))
	thinkingLevel, ok := normalizeThinkingLevel(thinkingCandidate)
	if !ok {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  fmt.Sprintf("Invalid thinking level %q. Use one of: off, minimal, low, medium, high, xhigh.", thinkingCandidate),
		}), nil
	}
	reasoningEffort := mapThinkingToReasoningEffort(thinkingLevel)

	modelCandidate := firstNonEmptyTrimmed(modelOverride, subagentModel(targetAgent, defaultSubagents))

	resolvedModel := ""
	modelWarning := ""
	modelApplied := false
	if modelCandidate != "" {
		resolved, valid, err := oc.resolveModelID(ctx, modelCandidate)
		if err != nil {
			modelWarning = err.Error()
		}
		if valid && resolved != "" {
			resolvedModel = resolved
			modelApplied = true
		} else if modelWarning == "" {
			modelWarning = fmt.Sprintf("That model isn't available: %s", modelCandidate)
		}
	}

	chatResp, err := oc.createAgentChatWithModel(ctx, targetAgent, resolvedModel, modelApplied)
	if err != nil {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  err.Error(),
		}), nil
	}
	if chatResp == nil {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  "failed to create sub-agent session",
		}), nil
	}

	childPortal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, chatResp.PortalKey)
	if err != nil || childPortal == nil {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  "failed to load sub-agent session",
		}), nil
	}

	childMeta := portalMeta(childPortal)
	childMeta.SubagentParentRoomID = portal.MXID.String()
	if reasoningEffort != "" {
		childMeta.RuntimeReasoning = reasoningEffort
	}

	roomName := resolveSubagentRoomName(label, task)
	if roomName != "" {
		childMeta.Title = roomName
		childPortal.Name = roomName
		childPortal.NameSet = true
		if chatResp.PortalInfo != nil {
			chatResp.PortalInfo.Name = &roomName
		}
	}
	oc.savePortalQuiet(ctx, childPortal, "subagent spawn metadata")

	if err := childPortal.CreateMatrixRoom(ctx, oc.UserLogin, chatResp.PortalInfo); err != nil {
		cleanupPortal(ctx, oc, childPortal, "failed to create subagent Matrix room")
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  err.Error(),
		}), nil
	}

	oc.sendWelcomeMessage(ctx, childPortal)
	if roomName != "" {
		if err := oc.setRoomNameNoSave(ctx, childPortal, roomName); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to set subagent room name")
		}
	}

	eventID := bridgeadapter.NewEventID("subagent")
	promptMessages, err := oc.buildPrompt(ctx, childPortal, childMeta, task, eventID)
	if err != nil {
		return tools.JSONResult(map[string]any{
			"status": "error",
			"error":  err.Error(),
		}), nil
	}

	userMessage := &database.Message{
		ID:       bridgeadapter.MatrixMessageID(eventID),
		MXID:     eventID,
		Room:     childPortal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: bridgeadapter.BaseMessageMetadata{Role: "user", Body: task},
		},
		Timestamp: time.Now(),
	}
	ensureCanonicalUserMessage(userMessage)
	if _, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userMessage.SenderID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure user ghost before saving subagent task message")
	}
	if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, userMessage); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to store subagent task message")
	}

	runID := uuid.NewString()
	run := &subagentRun{
		RunID:        runID,
		ChildRoomID:  childPortal.MXID,
		ParentRoomID: portal.MXID,
		Label:        label,
		Task:         task,
		Cleanup:      cleanup,
		StartedAt:    time.Now(),
		Timeout:      runTimeout,
	}
	oc.registerSubagentRun(run)
	oc.startSubagentRun(ctx, run, childPortal, childMeta, promptMessages)

	payload := map[string]any{
		"status":          "accepted",
		"childSessionKey": childPortal.MXID.String(),
		"runId":           runID,
	}
	if modelCandidate != "" {
		payload["modelApplied"] = modelApplied
	}
	if modelWarning != "" {
		payload["warning"] = modelWarning
	}

	return tools.JSONResult(payload), nil
}

func (oc *AIClient) startSubagentRun(
	ctx context.Context,
	run *subagentRun,
	childPortal *bridgev2.Portal,
	childMeta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
) {
	if run == nil || childPortal == nil || childMeta == nil {
		return
	}
	go oc.runSubagentAndAnnounce(ctx, run, childPortal, childMeta, prompt)
}
