package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
)

func (oc *AIClient) buildStatusText(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	isGroup bool,
	queueSettings airuntime.QueueSettings,
) string {
	if meta == nil || portal == nil {
		return "Status unavailable"
	}
	var sb strings.Builder
	sb.WriteString("Status\n")

	responder := oc.responderForMeta(ctx, meta)
	modelID := ""
	if responder != nil {
		modelID = responder.ModelID
	}
	provider := strings.TrimSpace(oc.responderProvider(responder))
	switch {
	case modelID == "" && provider != "":
		sb.WriteString(fmt.Sprintf("Model: %s\n", provider))
	case modelID == "":
		sb.WriteString("Model: unknown\n")
	case provider != "":
		sb.WriteString(fmt.Sprintf("Model: %s/%s\n", provider, modelID))
	default:
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

	contextWindow := 128000
	if responder != nil && responder.ContextLimit > 0 {
		contextWindow = responder.ContextLimit
	}
	if estimate := oc.estimatePromptTokens(ctx, portal, meta); estimate > 0 {
		sb.WriteString(fmt.Sprintf(
			"Context: %s/%s (%s)\n",
			formatCompactTokens(int64(estimate)),
			formatCompactTokens(int64(contextWindow)),
			formatPercent(estimate, contextWindow),
		))
	} else {
		sb.WriteString(fmt.Sprintf("Context: %s tokens\n", formatCompactTokens(int64(contextWindow))))
	}

	if isGroup {
		activation := oc.resolveGroupActivation(meta)
		sb.WriteString(fmt.Sprintf("Group activation: %s\n", activation))
	}

	caps := oc.getRoomCapabilities(ctx, meta)
	sb.WriteString(fmt.Sprintf(
		"Features: tools=%t vision=%t audio=%t video=%t pdf=%t\n",
		caps.SupportsToolCalling,
		caps.SupportsVision,
		caps.SupportsAudio,
		caps.SupportsVideo,
		caps.SupportsPDF,
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

type assistantUsageSnapshot struct {
	promptTokens     int64
	completionTokens int64
}

func (oc *AIClient) lastAssistantUsage(ctx context.Context, portal *bridgev2.Portal) *assistantUsageSnapshot {
	if oc == nil || portal == nil {
		return nil
	}
	history, err := oc.getAIHistoryMessages(ctx, portal, 50)
	if err != nil {
		return nil
	}
	for i := 0; i < len(history); i++ {
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
	return 0
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
