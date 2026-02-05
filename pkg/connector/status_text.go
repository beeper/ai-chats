package connector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
)

func (oc *AIClient) buildStatusText(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	isGroup bool,
	queueSettings QueueSettings,
) string {
	if meta == nil || portal == nil {
		return "Status unavailable."
	}
	var sb strings.Builder
	sb.WriteString("Status\n")

	modelID := oc.effectiveModel(meta)
	provider := strings.TrimSpace(loginMetadata(oc.UserLogin).Provider)
	if provider != "" {
		sb.WriteString(fmt.Sprintf("Model: %s/%s\n", provider, modelID))
	} else {
		sb.WriteString(fmt.Sprintf("Model: %s\n", modelID))
	}

	if usage := oc.lastAssistantUsage(ctx, portal); usage != nil {
		promptTokens := usage.promptTokens
		completionTokens := usage.completionTokens
		totalTokens := promptTokens + completionTokens
		if promptTokens > 0 || completionTokens > 0 {
			sb.WriteString(fmt.Sprintf(
				"Usage: prompt=%s completion=%s total=%s\n",
				formatCompactTokens(promptTokens),
				formatCompactTokens(completionTokens),
				formatCompactTokens(totalTokens),
			))
		}
	}

	contextWindow := oc.getModelContextWindow(meta)
	if estimate := oc.estimatePromptTokens(ctx, portal, meta); estimate > 0 {
		sb.WriteString(fmt.Sprintf(
			"Context: %s/%s (%s) compactions=%d\n",
			formatCompactTokens(int64(estimate)),
			formatCompactTokens(int64(contextWindow)),
			formatPercent(estimate, contextWindow),
			meta.CompactionCount,
		))
	} else {
		sb.WriteString(fmt.Sprintf("Context: %s tokens compactions=%d\n", formatCompactTokens(int64(contextWindow)), meta.CompactionCount))
	}

	sessionKey := portal.MXID.String()
	agentID := resolveAgentID(meta)
	entry := oc.getSessionEntryMaybe(ctx, agentID, sessionKey)
	if entry != nil && entry.UpdatedAt > 0 {
		sb.WriteString(fmt.Sprintf("Session: %s (updated %s)\n", sessionKey, formatAge(time.Now().UnixMilli()-entry.UpdatedAt)))
	} else if sessionKey != "" {
		sb.WriteString(fmt.Sprintf("Session: %s\n", sessionKey))
	}

	if meta.SessionResetAt > 0 {
		ts := time.UnixMilli(meta.SessionResetAt).Format(time.RFC3339)
		sb.WriteString(fmt.Sprintf("Session reset: %s\n", ts))
	}

	if isGroup {
		activation := oc.resolveGroupActivation(meta)
		sb.WriteString(fmt.Sprintf("Group activation: %s\n", activation))
	}

	thinking := meta.ThinkingLevel
	if thinking == "" {
		if meta.EmitThinking {
			thinking = "on"
		} else {
			thinking = "off"
		}
	}
	reasoning := strings.TrimSpace(meta.ReasoningEffort)
	if reasoning == "" {
		if meta.EmitThinking {
			reasoning = "on"
		} else {
			reasoning = "off"
		}
	}
	verbose := strings.TrimSpace(meta.VerboseLevel)
	if verbose == "" {
		verbose = "off"
	}
	elevated := strings.TrimSpace(meta.ElevatedLevel)
	if elevated == "" {
		elevated = "off"
	}
	sendPolicy := normalizeSendPolicyMode(meta.SendPolicy)
	if sendPolicy == "" {
		sendPolicy = "allow"
	}
	sendLabel := "on"
	if sendPolicy == "deny" {
		sendLabel = "off"
	}
	responseMode := string(oc.getAgentResponseMode(meta))
	conversationMode := strings.TrimSpace(meta.ConversationMode)
	if conversationMode == "" {
		conversationMode = "default"
	}
	sb.WriteString(fmt.Sprintf(
		"Options: think=%s reasoning=%s verbose=%s elevated=%s send=%s response=%s conversation=%s\n",
		thinking,
		reasoning,
		verbose,
		elevated,
		sendLabel,
		responseMode,
		conversationMode,
	))

	queueDepth := 0
	queueDropped := 0
	if snapshot := oc.getQueueSnapshot(portal.MXID); snapshot != nil {
		queueDepth = len(snapshot.items)
		queueDropped = snapshot.droppedCount
	}
	queueLine := fmt.Sprintf(
		"Queue: mode=%s depth=%d debounce=%dms cap=%d drop=%s",
		queueSettings.Mode,
		queueDepth,
		queueSettings.DebounceMs,
		queueSettings.Cap,
		queueSettings.DropPolicy,
	)
	if queueDropped > 0 {
		queueLine = fmt.Sprintf("%s dropped=%d", queueLine, queueDropped)
	}
	sb.WriteString(queueLine + "\n")

	typingCtx := &TypingContext{IsGroup: isGroup, WasMentioned: !isGroup}
	typingMode := oc.resolveTypingMode(meta, typingCtx, false)
	typingInterval := oc.resolveTypingInterval(meta)
	typingLine := fmt.Sprintf(
		"Typing: mode=%s interval=%s",
		typingMode,
		formatTypingInterval(typingInterval),
	)
	if meta.TypingMode != "" || meta.TypingIntervalSeconds != nil {
		overrideMode := "default"
		if meta.TypingMode != "" {
			overrideMode = meta.TypingMode
		}
		overrideInterval := "default"
		if meta.TypingIntervalSeconds != nil {
			overrideInterval = fmt.Sprintf("%ds", *meta.TypingIntervalSeconds)
		}
		typingLine = fmt.Sprintf("%s (session override: mode=%s interval=%s)", typingLine, overrideMode, overrideInterval)
	}
	sb.WriteString(typingLine + "\n")

	return strings.TrimSpace(sb.String())
}

func formatTypingInterval(interval time.Duration) string {
	if interval <= 0 {
		return "off"
	}
	seconds := int(interval.Seconds())
	if seconds <= 0 {
		seconds = 1
	}
	return fmt.Sprintf("%ds", seconds)
}

func (oc *AIClient) buildContextStatus(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) string {
	if meta == nil || portal == nil {
		return "Context unavailable."
	}
	var sb strings.Builder
	sb.WriteString("Context\n")
	modelID := oc.effectiveModel(meta)
	provider := strings.TrimSpace(loginMetadata(oc.UserLogin).Provider)
	if provider != "" {
		sb.WriteString(fmt.Sprintf("Model: %s/%s\n", provider, modelID))
	} else {
		sb.WriteString(fmt.Sprintf("Model: %s\n", modelID))
	}

	contextWindow := oc.getModelContextWindow(meta)
	estimate := oc.estimatePromptTokens(ctx, portal, meta)
	if estimate > 0 {
		sb.WriteString(fmt.Sprintf(
			"Prompt estimate: %s/%s (%s)\n",
			formatCompactTokens(int64(estimate)),
			formatCompactTokens(int64(contextWindow)),
			formatPercent(estimate, contextWindow),
		))
	} else {
		sb.WriteString(fmt.Sprintf("Context window: %s tokens\n", formatCompactTokens(int64(contextWindow))))
	}

	systemPrompt := oc.effectivePrompt(meta)
	if systemPrompt != "" {
		sysTokens := 0
		if count, err := EstimateTokens([]openai.ChatCompletionMessageParamUnion{openai.SystemMessage(systemPrompt)}, modelID); err == nil {
			sysTokens = count
		}
		sysLine := fmt.Sprintf("System prompt: %d chars", len(systemPrompt))
		if sysTokens > 0 {
			sysLine = fmt.Sprintf("%s (%s tokens)", sysLine, formatCompactTokens(int64(sysTokens)))
		}
		sb.WriteString(sysLine + "\n")
	}

	historyLimit := oc.historyLimit(meta)
	historyCount := 0
	if historyLimit > 0 {
		if history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, historyLimit); err == nil {
			historyCount = len(history)
		}
	}
	sb.WriteString(fmt.Sprintf("History limit: %d messages\n", historyLimit))
	sb.WriteString(fmt.Sprintf("History loaded: %d messages\n", historyCount))

	sb.WriteString(fmt.Sprintf("Compactions: %d\n", meta.CompactionCount))

	if meta.SessionResetAt > 0 {
		sb.WriteString(fmt.Sprintf("Session reset: %s\n", time.UnixMilli(meta.SessionResetAt).Format(time.RFC3339)))
	}
	if strings.TrimSpace(meta.ConversationMode) != "" {
		sb.WriteString(fmt.Sprintf("Conversation mode: %s\n", meta.ConversationMode))
	}
	if meta.LastResponseID != "" {
		sb.WriteString(fmt.Sprintf("Last response ID: %s\n", meta.LastResponseID))
	}

	return strings.TrimSpace(sb.String())
}

type assistantUsageSnapshot struct {
	promptTokens     int64
	completionTokens int64
}

func (oc *AIClient) lastAssistantUsage(ctx context.Context, portal *bridgev2.Portal) *assistantUsageSnapshot {
	if oc == nil || portal == nil {
		return nil
	}
	history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 50)
	if err != nil {
		return nil
	}
	for i := len(history) - 1; i >= 0; i-- {
		meta := messageMeta(history[i])
		if meta == nil || meta.Role != "assistant" {
			continue
		}
		if meta.PromptTokens == 0 && meta.CompletionTokens == 0 {
			continue
		}
		return &assistantUsageSnapshot{
			promptTokens:     meta.PromptTokens,
			completionTokens: meta.CompletionTokens,
		}
	}
	return nil
}

func (oc *AIClient) estimatePromptTokens(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) int {
	if oc == nil || portal == nil {
		return 0
	}
	prompt, err := oc.buildBasePrompt(ctx, portal, meta)
	if err != nil {
		return 0
	}
	prompt = oc.injectMemoryContext(ctx, portal, meta, prompt)
	count, err := EstimateTokens(prompt, oc.effectiveModel(meta))
	if err != nil {
		return 0
	}
	return count
}

func (oc *AIClient) getSessionEntryMaybe(ctx context.Context, agentID, sessionKey string) *sessionEntry {
	if oc == nil || sessionKey == "" {
		return nil
	}
	ref := oc.resolveSessionStoreRef(agentID)
	if entry, ok := oc.getSessionEntry(ctx, ref, sessionKey); ok {
		return &entry
	}
	return nil
}

func formatCompactTokens(value int64) string {
	abs := value
	if abs < 0 {
		abs = -abs
	}
	if abs >= 1_000_000 {
		return fmt.Sprintf("%.1fm", float64(value)/1_000_000)
	}
	if abs >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	}
	return fmt.Sprintf("%d", value)
}

func formatPercent(numerator, denominator int) string {
	if denominator <= 0 || numerator <= 0 {
		return "0%"
	}
	percent := (float64(numerator) / float64(denominator)) * 100
	return fmt.Sprintf("%.0f%%", percent)
}

func formatAge(deltaMs int64) string {
	if deltaMs < 0 {
		deltaMs = -deltaMs
	}
	d := time.Duration(deltaMs) * time.Millisecond
	if d < time.Minute {
		secs := int(d.Seconds())
		if secs <= 0 {
			secs = 1
		}
		return fmt.Sprintf("%ds ago", secs)
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func (oc *AIClient) buildToolsStatusText(meta *PortalMetadata) string {
	var sb strings.Builder
	sb.WriteString("Tool Status:\n\n")

	toolsList := oc.buildAvailableTools(meta)
	sort.Slice(toolsList, func(i, j int) bool {
		return toolsList[i].Name < toolsList[j].Name
	})

	sb.WriteString("Tools:\n")
	for _, tool := range toolsList {
		status := "✗"
		if tool.Enabled {
			status = "✓"
		}
		desc := tool.Description
		if desc == "" {
			desc = tool.DisplayName
		}
		reason := ""
		if !tool.Enabled && tool.Reason != "" {
			reason = fmt.Sprintf(" (%s)", tool.Reason)
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s: %s%s\n", status, tool.Name, desc, reason))
	}

	if meta != nil && !meta.Capabilities.SupportsToolCalling {
		sb.WriteString(fmt.Sprintf("\nNote: Current model (%s) may not support tool calling.\n", oc.effectiveModel(meta)))
	}

	return strings.TrimSpace(sb.String())
}
