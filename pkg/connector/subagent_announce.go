package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func formatDurationShort(valueMs int64) string {
	if valueMs <= 0 {
		return ""
	}
	totalSeconds := int64((valueMs + 500) / 1000)
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func formatTokenCount(value int64) string {
	if value <= 0 {
		return "0"
	}
	if value >= 1_000_000 {
		return fmt.Sprintf("%.1fm", float64(value)/1_000_000)
	}
	if value >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	}
	return fmt.Sprintf("%d", value)
}

func (oc *AIClient) readLatestAssistantReply(ctx context.Context, portal *bridgev2.Portal) string {
	if oc == nil || portal == nil {
		return ""
	}
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 50)
	if err != nil || len(messages) == 0 {
		return ""
	}
	for i := len(messages) - 1; i >= 0; i-- {
		meta := messageMeta(messages[i])
		if meta == nil || meta.Role != "assistant" {
			continue
		}
		body := strings.TrimSpace(meta.Body)
		if body != "" {
			return body
		}
	}
	return ""
}

func (oc *AIClient) resolveUsageFromMessages(messages []*database.Message) (int64, int64, int64) {
	var inputTokens int64
	var outputTokens int64
	var totalTokens int64
	for i := len(messages) - 1; i >= 0; i-- {
		meta := messageMeta(messages[i])
		if meta == nil || meta.Role != "assistant" {
			continue
		}
		if meta.PromptTokens > 0 || meta.CompletionTokens > 0 {
			inputTokens = meta.PromptTokens
			outputTokens = meta.CompletionTokens
			totalTokens = meta.PromptTokens + meta.CompletionTokens
			break
		}
	}
	return inputTokens, outputTokens, totalTokens
}

func (oc *AIClient) buildSubagentStatsLine(ctx context.Context, portal *bridgev2.Portal, run *subagentRun, endedAt time.Time) string {
	if oc == nil || portal == nil || run == nil {
		return ""
	}
	var runtimeMs int64
	if !run.StartedAt.IsZero() && !endedAt.IsZero() {
		runtimeMs = endedAt.Sub(run.StartedAt).Milliseconds()
	}
	messages, _ := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 200)
	inputTokens, outputTokens, totalTokens := oc.resolveUsageFromMessages(messages)

	var parts []string
	runtime := formatDurationShort(runtimeMs)
	if runtime == "" {
		parts = append(parts, "runtime n/a")
	} else {
		parts = append(parts, fmt.Sprintf("runtime %s", runtime))
	}
	if totalTokens > 0 {
		parts = append(parts, fmt.Sprintf("tokens %s (in %s / out %s)", formatTokenCount(totalTokens), formatTokenCount(inputTokens), formatTokenCount(outputTokens)))
	} else {
		parts = append(parts, "tokens n/a")
	}

	sessionKey := portal.MXID.String()
	if sessionKey != "" {
		parts = append(parts, fmt.Sprintf("sessionKey %s", sessionKey))
	}
	sessionID := string(portal.PortalKey.ID)
	if sessionID != "" {
		parts = append(parts, fmt.Sprintf("sessionId %s", sessionID))
	}

	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("Stats: %s", strings.Join(parts, " • "))
}

func (oc *AIClient) runSubagentCompletion(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
) (bool, error) {
	modelChain := oc.modelFallbackChain(ctx, meta)
	if len(modelChain) == 0 {
		modelChain = []string{oc.effectiveModel(meta)}
	}

	for idx, modelID := range modelChain {
		effectiveMeta := meta
		if meta != nil {
			effectiveMeta = oc.overrideModel(meta, modelID)
		}
		responseFn, logLabel := oc.selectResponseFn(effectiveMeta, prompt)
		success, err := oc.responseWithRetryAndReasoningFallback(ctx, nil, portal, effectiveMeta, prompt, responseFn, logLabel)
		if success {
			return true, nil
		}
		if err == nil {
			return false, nil
		}
		if !shouldFallbackOnError(err) || idx == len(modelChain)-1 {
			return false, err
		}
		oc.loggerForContext(ctx).Warn().
			Err(err).
			Str("failed_model", modelID).
			Str("next_model", modelChain[idx+1]).
			Msg("Subagent model failed; falling back to next model")
	}
	return false, nil
}

func (oc *AIClient) runSubagentAndAnnounce(
	ctx context.Context,
	run *subagentRun,
	childPortal *bridgev2.Portal,
	childMeta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
) {
	if oc == nil || run == nil || childPortal == nil || childMeta == nil {
		return
	}
	defer oc.unregisterSubagentRun(run.RunID)

	runCtx := oc.backgroundContext(ctx)
	if run.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, run.Timeout)
		defer cancel()
	}

	success, err := oc.runSubagentCompletion(runCtx, childPortal, childMeta, prompt)
	endedAt := time.Now()

	outcomeStatus := "unknown"
	outcomeError := ""
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		outcomeStatus = "timeout"
	case err != nil:
		outcomeStatus = "error"
		outcomeError = err.Error()
	case success:
		outcomeStatus = "ok"
	}

	reply := oc.readLatestAssistantReply(runCtx, childPortal)
	if strings.TrimSpace(reply) == "" {
		reply = "(no output)"
	}

	statsLine := oc.buildSubagentStatsLine(runCtx, childPortal, run, endedAt)

	statusLabel := "finished with unknown status"
	switch outcomeStatus {
	case "ok":
		statusLabel = "completed successfully"
	case "timeout":
		statusLabel = "timed out"
	case "error":
		if strings.TrimSpace(outcomeError) != "" {
			statusLabel = fmt.Sprintf("failed: %s", strings.TrimSpace(outcomeError))
		} else {
			statusLabel = "failed: unknown error"
		}
	}

	taskLabel := strings.TrimSpace(run.Label)
	if taskLabel == "" {
		taskLabel = strings.TrimSpace(run.Task)
	}
	if taskLabel == "" {
		taskLabel = "background task"
	}

	triggerMessage := strings.Join([]string{
		fmt.Sprintf("A background task \"%s\" just %s.", taskLabel, statusLabel),
		"",
		"Findings:",
		reply,
		"",
		statsLine,
		"",
		"Summarize this naturally for the user. Keep it brief (1-2 sentences). Flow it into the conversation naturally.",
		"Do not mention technical details like tokens, stats, or that this was a background task.",
		"You can respond with NO_REPLY if no announcement is needed (e.g., internal task with no user-facing result).",
	}, "\n")

	parentPortal := oc.portalByRoomID(runCtx, id.RoomID(run.ParentRoomID))
	if parentPortal != nil {
		parentMeta := portalMeta(parentPortal)
		if parentMeta != nil {
			if _, _, err := oc.dispatchInternalMessage(runCtx, parentPortal, parentMeta, triggerMessage, "subagent", true); err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to dispatch subagent announce trigger")
			}
		}
	}

	if strings.TrimSpace(run.Cleanup) == "delete" {
		cleanupPortal(runCtx, oc, childPortal, "subagent cleanup")
	}
}
