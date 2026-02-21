package connector

import (
	"context"
	"errors"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
)

const (
	cronDeliveryTimeout = 10 * time.Second
)

func (oc *AIClient) runCronIsolatedAgentJob(ctx context.Context, job integrationcron.Job, message string) (status string, summary string, outputText string, err error) {
	if oc == nil || oc.UserLogin == nil {
		return "error", "", "", errors.New("missing client")
	}
	return integrationcron.RunCronIsolatedAgentJob(ctx, job, message, integrationcron.IsolatedRunnerDeps{
		DeliveryTimeout: cronDeliveryTimeout,
		MergeContext:    oc.mergeCronContext,
		ResolveAgentID: func(raw string) string {
			return resolveCronAgentID(raw, &oc.connector.Config)
		},
		GetOrCreateRoom: func(ctx context.Context, agentID, jobID, jobName string) (any, error) {
			return oc.getOrCreateCronRoom(ctx, agentID, jobID, jobName)
		},
		BuildDispatchMetadata: func(room any, patch integrationcron.MetadataPatch) any {
			portal, _ := room.(*bridgev2.Portal)
			return oc.buildCronDispatchMetadata(portal, patch)
		},
		NormalizeThinkingLevel: normalizeThinkingLevel,
		SessionKey:             cronSessionKey,
		UpdateSessionEntry: func(ctx context.Context, sessionKey string, updater func(entry integrationcron.SessionEntry) integrationcron.SessionEntry) {
			oc.updateCronSessionEntry(ctx, sessionKey, updater)
		},
		ResolveUserTimezone: func() string {
			tz, _ := oc.resolveUserTimezone()
			return tz
		},
		LastAssistantMessage: func(ctx context.Context, room any) (string, int64) {
			portal, _ := room.(*bridgev2.Portal)
			return oc.lastAssistantMessageInfo(ctx, portal)
		},
		DispatchInternalMessage: func(ctx context.Context, room any, metadata any, message string) error {
			portal, _ := room.(*bridgev2.Portal)
			if portal == nil {
				return errors.New("missing portal")
			}
			metaSnapshot, _ := metadata.(*PortalMetadata)
			if metaSnapshot == nil {
				metaSnapshot = &PortalMetadata{}
			}
			_, _, dispatchErr := oc.dispatchInternalMessage(ctx, portal, metaSnapshot, message, "cron", false)
			return dispatchErr
		},
		WaitForAssistantMessage: func(ctx context.Context, room any, lastID string, lastTimestamp int64) (integrationcron.AssistantMessage, bool) {
			portal, _ := room.(*bridgev2.Portal)
			msg, found := oc.waitForNewAssistantMessage(ctx, portal, lastID, lastTimestamp)
			if !found || msg == nil {
				return integrationcron.AssistantMessage{}, false
			}
			body := ""
			model := ""
			var promptTokens, completionTokens int64
			if meta := messageMeta(msg); meta != nil {
				body = strings.TrimSpace(meta.Body)
				model = strings.TrimSpace(meta.Model)
				promptTokens = meta.PromptTokens
				completionTokens = meta.CompletionTokens
			}
			return integrationcron.AssistantMessage{
				Body:             body,
				Model:            model,
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
			}, true
		},
		ResolveAckMaxChars: func(agentID string) int {
			return resolveHeartbeatAckMaxChars(&oc.connector.Config, resolveHeartbeatConfig(&oc.connector.Config, agentID))
		},
		ResolveDeliveryTarget: func(agentID string, delivery *integrationcron.Delivery) integrationcron.DeliveryTarget {
			target := oc.resolveCronDeliveryTarget(agentID, delivery)
			return integrationcron.DeliveryTarget{
				Portal:  target.Portal,
				RoomID:  target.RoomID.String(),
				Channel: target.Channel,
				Reason:  target.Reason,
			}
		},
		SendDeliveryMessage: func(ctx context.Context, portal any, body string) error {
			targetPortal, _ := portal.(*bridgev2.Portal)
			if targetPortal == nil {
				return errors.New("missing delivery portal")
			}
			return oc.sendPlainAssistantMessageWithResult(ctx, targetPortal, body)
		},
	})
}

func (oc *AIClient) buildCronDispatchMetadata(portal *bridgev2.Portal, patch integrationcron.MetadataPatch) *PortalMetadata {
	meta := portalMeta(portal)
	metaSnapshot := clonePortalMetadata(meta)
	if metaSnapshot == nil {
		metaSnapshot = &PortalMetadata{}
	}
	metaSnapshot.AgentID = patch.AgentID
	if patch.Model != nil {
		metaSnapshot.Model = strings.TrimSpace(*patch.Model)
	}
	if patch.ReasoningEffort != nil {
		metaSnapshot.ReasoningEffort = strings.TrimSpace(*patch.ReasoningEffort)
	}
	if patch.DisableMessageTool {
		metaSnapshot.DisabledTools = []string{ToolNameMessage}
	}
	return metaSnapshot
}

// mergeCronContext ensures cron runs are cancelled on disconnect while preserving deadlines.
func (oc *AIClient) mergeCronContext(ctx context.Context) (context.Context, context.CancelFunc) {
	var base context.Context
	if oc != nil && oc.disconnectCtx != nil {
		base = oc.disconnectCtx
	} else if oc != nil && oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.BackgroundCtx != nil {
		base = oc.UserLogin.Bridge.BackgroundCtx
	} else {
		base = context.Background()
	}

	if model, ok := modelOverrideFromContext(ctx); ok {
		base = withModelOverride(base, model)
	}

	var merged context.Context
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); ok {
		merged, cancel = context.WithDeadline(base, deadline)
	} else {
		merged, cancel = context.WithCancel(base)
	}
	return oc.loggerForContext(ctx).WithContext(merged), cancel
}

func (oc *AIClient) lastAssistantMessageInfo(ctx context.Context, portal *bridgev2.Portal) (string, int64) {
	if portal == nil {
		return "", 0
	}
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
