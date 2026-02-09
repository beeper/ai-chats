package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/google/uuid"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/cron"
)

const (
	defaultCronIsolatedTimeoutSeconds = 600
	noTimeoutMs                       = int64(30 * 24 * 60 * 60 * 1000)
	cronDeliveryTimeout               = 10 * time.Second
)

func (oc *AIClient) runCronIsolatedAgentJob(job cron.CronJob, message string) (status string, summary string, outputText string, err error) {
	if oc == nil || oc.UserLogin == nil {
		return "error", "", "", errors.New("missing client")
	}
	ctx := oc.backgroundContext(context.Background())
	agentID := resolveCronAgentID(job.AgentID, &oc.connector.Config)
	portal, err := oc.getOrCreateCronRoom(ctx, agentID, job.ID, job.Name)
	if err != nil {
		return "error", "", "", err
	}
	meta := portalMeta(portal)
	metaSnapshot := clonePortalMetadata(meta)
	if metaSnapshot == nil {
		metaSnapshot = &PortalMetadata{}
	}
	metaSnapshot.AgentID = agentID

	// Apply model override for this run if provided.
	if strings.TrimSpace(job.Payload.Model) != "" {
		metaSnapshot.Model = strings.TrimSpace(job.Payload.Model)
	}
	if strings.TrimSpace(job.Payload.Thinking) != "" {
		if level, ok := normalizeThinkingLevel(job.Payload.Thinking); ok {
			if level == "off" {
				metaSnapshot.ReasoningEffort = ""
			} else {
				metaSnapshot.ReasoningEffort = level
			}
		}
	}

	timeoutMs := resolveCronIsolatedTimeoutMs(job, &oc.connector.Config)

	sessionKey := cronSessionKey(agentID, job.ID)
	runID := uuid.NewString()
	oc.updateCronSessionEntry(ctx, sessionKey, func(entry cronSessionEntry) cronSessionEntry {
		entry.SessionID = runID
		entry.UpdatedAt = time.Now().UnixMilli()
		return entry
	})

	userTimezone, _ := oc.resolveUserTimezone()
	cronMessage := buildCronMessage(job.ID, job.Name, message, userTimezone)

	if job.Payload.AllowUnsafeExternal == nil || !*job.Payload.AllowUnsafeExternal {
		cronMessage = wrapSafeExternalPrompt(cronMessage)
	}

	// Resolve delivery mode early so we can disable the message tool when
	// delivery is planned (the cron runner handles delivery itself).
	delivery := job.Delivery
	deliveryMode := cron.CronDeliveryAnnounce
	if delivery != nil && strings.TrimSpace(string(delivery.Mode)) != "" {
		deliveryMode = delivery.Mode
	}
	if delivery == nil {
		delivery = &cron.CronDelivery{Mode: deliveryMode}
	}
	if deliveryMode == cron.CronDeliveryAnnounce {
		metaSnapshot.DisabledTools = []string{ToolNameMessage}
	}

	// Capture last assistant message before dispatch.
	lastID, lastTimestamp := oc.lastAssistantMessageInfo(ctx, portal)

	_, _, dispatchErr := oc.dispatchInternalMessage(ctx, portal, metaSnapshot, cronMessage, "cron", false)
	if dispatchErr != nil {
		return "error", "", "", dispatchErr
	}

	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		msg, found := oc.waitForNewAssistantMessage(ctx, portal, lastID, lastTimestamp)
		if found {
			body := ""
			if msg != nil {
				if meta := messageMeta(msg); meta != nil {
					body = strings.TrimSpace(meta.Body)
					oc.updateCronSessionEntry(ctx, sessionKey, func(entry cronSessionEntry) cronSessionEntry {
						entry.Model = strings.TrimSpace(meta.Model)
						entry.PromptTokens = meta.PromptTokens
						entry.CompletionTokens = meta.CompletionTokens
						total := meta.PromptTokens + meta.CompletionTokens
						if total > 0 {
							entry.TotalTokens = total
						}
						entry.UpdatedAt = time.Now().UnixMilli()
						return entry
					})
				}
			}
			outputText = body
			summary = truncateTextForCronSummary(body)
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if outputText == "" {
		return "error", "", "", errors.New("cron job timed out")
	}

	deliveryRequested := deliveryMode == cron.CronDeliveryAnnounce
	bestEffort := delivery != nil && delivery.BestEffort != nil && *delivery.BestEffort

	ackMax := resolveHeartbeatAckMaxChars(&oc.connector.Config, resolveHeartbeatConfig(&oc.connector.Config, agentID))
	skipHeartbeatDelivery := deliveryRequested && isHeartbeatOnlyText(outputText, ackMax)

	if deliveryRequested && !skipHeartbeatDelivery {
		target := oc.resolveCronDeliveryTarget(agentID, delivery)
		if target.Portal == nil || target.RoomID == "" {
			reason := strings.TrimSpace(target.Reason)
			if reason == "" {
				reason = "no-target"
			}
			if bestEffort {
				return "skipped", fmt.Sprintf("Delivery skipped (%s).", reason), outputText, nil
			}
			return "error", summary, outputText, fmt.Errorf("cron delivery failed: %s", reason)
		}
		if strings.TrimSpace(outputText) != "" {
			// Bound delivery time. A blocked Matrix send can otherwise wedge the cron scheduler
			// (which runs jobs inline on the timer goroutine).
			deliveryCtx, cancel := context.WithTimeout(ctx, cronDeliveryTimeout)
			defer cancel()
			if sendErr := oc.sendPlainAssistantMessageWithResult(deliveryCtx, target.Portal, outputText); sendErr != nil {
				if bestEffort {
					return "skipped", fmt.Sprintf("Delivery skipped (%s).", sendErr.Error()), outputText, nil
				}
				return "error", summary, outputText, fmt.Errorf("cron delivery failed: %w", sendErr)
			}
		}
	}

	return "ok", summary, outputText, nil
}

func resolveCronIsolatedTimeoutMs(job cron.CronJob, cfg *Config) int64 {
	defaultSeconds := defaultCronIsolatedTimeoutSeconds
	if cfg != nil && cfg.Agents != nil && cfg.Agents.Defaults != nil && cfg.Agents.Defaults.TimeoutSeconds > 0 {
		defaultSeconds = cfg.Agents.Defaults.TimeoutSeconds
	}
	timeoutSeconds := defaultSeconds
	if job.Payload.TimeoutSeconds != nil {
		overrideSeconds := *job.Payload.TimeoutSeconds
		switch {
		case overrideSeconds == 0:
			return noTimeoutMs
		case overrideSeconds > 0:
			timeoutSeconds = overrideSeconds
		}
	}
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}
	return int64(timeoutSeconds) * 1000
}

func (oc *AIClient) lastAssistantMessageInfo(ctx context.Context, portal *bridgev2.Portal) (string, int64) {
	if portal == nil {
		return "", 0
	}
	// Don't assume DB ordering (some implementations return newest-first).
	// Scan for the newest assistant message by timestamp.
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 20)
	if err != nil {
		return "", 0
	}
	bestID := ""
	bestTS := int64(0)
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		meta := messageMeta(msg)
		if meta == nil || meta.Role != "assistant" {
			continue
		}
		ts := msg.Timestamp.UnixMilli()
		if bestID == "" || ts > bestTS {
			bestID = msg.MXID.String()
			bestTS = ts
		}
	}
	return bestID, bestTS
}

func (oc *AIClient) waitForNewAssistantMessage(ctx context.Context, portal *bridgev2.Portal, lastID string, lastTimestamp int64) (*database.Message, bool) {
	if portal == nil {
		return nil, false
	}
	// Don't assume DB ordering (some implementations return newest-first).
	// Pick the newest assistant message that is strictly newer than the last
	// snapshot, or has a different event ID at the same timestamp.
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 20)
	if err != nil {
		return nil, false
	}
	var candidate *database.Message
	candidateTS := lastTimestamp
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		meta := messageMeta(msg)
		if meta == nil || meta.Role != "assistant" {
			continue
		}
		idStr := msg.MXID.String()
		ts := msg.Timestamp.UnixMilli()
		if ts < lastTimestamp {
			continue
		}
		if ts == lastTimestamp && idStr == lastID {
			continue
		}
		// Prefer the newest matching assistant message.
		if candidate == nil || ts > candidateTS {
			candidate = msg
			candidateTS = ts
		}
	}
	if candidate == nil {
		return nil, false
	}
	return candidate, true
}

func truncateTextForCronSummary(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	const max = 2000
	if len(trimmed) <= max {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:max]) + "…"
}

func isHeartbeatOnlyText(text string, ackMax int) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	shouldSkip, stripped, _ := agents.StripHeartbeatTokenWithMode(trimmed, agents.StripHeartbeatModeHeartbeat, ackMax)
	if shouldSkip && strings.TrimSpace(stripped) == "" {
		return true
	}
	return false
}
