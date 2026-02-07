package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/cron"
)

const (
	defaultCronIsolatedTimeoutSeconds = 600
	noTimeoutMs                       = int64(30 * 24 * 60 * 60 * 1000)
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
	runID := newCronSessionID()
	oc.updateCronSessionEntry(ctx, sessionKey, func(entry cronSessionEntry) cronSessionEntry {
		entry.SessionID = runID
		entry.UpdatedAt = cronSessionUpdatedAt()
		return entry
	})

	userTimezone, _ := oc.resolveUserTimezone()
	cronMessage := buildCronMessage(job.ID, job.Name, message, userTimezone)

	if job.Payload.AllowUnsafeExternal == nil || !*job.Payload.AllowUnsafeExternal {
		cronMessage = wrapSafeExternalPrompt(cronMessage)
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
						entry.UpdatedAt = cronSessionUpdatedAt()
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

	delivery := job.Delivery
	deliveryMode := cron.CronDeliveryAnnounce
	if delivery != nil && strings.TrimSpace(string(delivery.Mode)) != "" {
		deliveryMode = delivery.Mode
	}
	if delivery == nil {
		delivery = &cron.CronDelivery{Mode: deliveryMode}
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
			oc.sendPlainAssistantMessage(ctx, target.Portal, outputText)
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
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 5)
	if err != nil {
		return "", 0
	}
	for i := len(messages) - 1; i >= 0; i-- {
		meta := messageMeta(messages[i])
		if meta == nil || meta.Role != "assistant" {
			continue
		}
		return messages[i].MXID.String(), messages[i].Timestamp.UnixMilli()
	}
	return "", 0
}

func (oc *AIClient) waitForNewAssistantMessage(ctx context.Context, portal *bridgev2.Portal, lastID string, lastTimestamp int64) (*database.Message, bool) {
	if portal == nil {
		return nil, false
	}
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 5)
	if err != nil {
		return nil, false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		meta := messageMeta(msg)
		if meta == nil || meta.Role != "assistant" {
			continue
		}
		if msg.MXID.String() == lastID {
			return nil, false
		}
		if msg.Timestamp.UnixMilli() <= lastTimestamp {
			return nil, false
		}
		return msg, true
	}
	return nil, false
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
