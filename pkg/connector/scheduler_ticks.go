package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func (s *schedulerRuntime) scheduleTickLocked(ctx context.Context, roomID id.RoomID, content ScheduleTickContent, delay time.Duration) (*mautrix.RespSendEvent, error) {
	intent := s.intentClient()
	if intent == nil {
		return nil, errors.New("matrix intent not available")
	}
	if delay < scheduleImmediateDelay {
		delay = scheduleImmediateDelay
	}
	resp, err := intent.SendMessageEvent(ctx, roomID, ScheduleTickEventType, content, mautrix.ReqSendEvent{UnstableDelay: delay})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *schedulerRuntime) delayedEventExistsLocked(ctx context.Context, delayID string) (bool, error) {
	intent := s.intentClient()
	if intent == nil || strings.TrimSpace(delayID) == "" {
		return false, nil
	}
	resp, err := intent.DelayedEvents(ctx, &mautrix.ReqDelayedEvents{DelayID: id.DelayID(delayID)})
	if err != nil {
		return false, err
	}
	return resp != nil, nil
}

func (s *schedulerRuntime) cancelPendingDelayLocked(ctx context.Context, delayID string) error {
	intent := s.intentClient()
	if intent == nil || strings.TrimSpace(delayID) == "" {
		return nil
	}
	_, err := intent.UpdateDelayedEvent(ctx, &mautrix.ReqUpdateDelayedEvent{
		DelayID: id.DelayID(delayID),
		Action:  event.DelayActionCancel,
	})
	return err
}

func (s *schedulerRuntime) intentClient() schedulerDelayedEventIntent {
	if s == nil || s.client == nil || s.client.UserLogin == nil || s.client.UserLogin.Bridge == nil {
		return nil
	}
	return resolveSchedulerDelayedEventIntent(s.client.UserLogin)
}

func appendRunKey(existing []string, runKey string) []string {
	trimmed := strings.TrimSpace(runKey)
	if trimmed == "" {
		return existing
	}
	if containsRunKey(existing, trimmed) {
		return existing
	}
	existing = append(existing, trimmed)
	if len(existing) > 8 {
		existing = existing[len(existing)-8:]
	}
	return existing
}

func containsRunKey(existing []string, runKey string) bool {
	trimmedRunKey := strings.TrimSpace(runKey)
	for _, candidate := range existing {
		if strings.TrimSpace(candidate) == trimmedRunKey {
			return true
		}
	}
	return false
}

func resolveScheduledCronTimeoutSeconds(client *AIClient, override *int) int {
	if override != nil {
		if *override == 0 {
			return 30 * 24 * 60 * 60
		}
		if *override > 0 {
			return *override
		}
	}
	if client != nil && client.connector != nil && client.connector.Config.Agents != nil && client.connector.Config.Agents.Defaults != nil && client.connector.Config.Agents.Defaults.TimeoutSeconds > 0 {
		return client.connector.Config.Agents.Defaults.TimeoutSeconds
	}
	return defaultCronTimeoutSeconds
}

func truncateSchedulePreview(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	runes := []rune(text)
	if len(runes) <= 160 {
		return text
	}
	return strings.TrimSpace(string(runes[:159])) + "..."
}

func appendMissingDisabledTool(existing []string, toolName string) []string {
	for _, entry := range existing {
		if strings.EqualFold(strings.TrimSpace(entry), strings.TrimSpace(toolName)) {
			return existing
		}
	}
	return append(existing, toolName)
}

func parseScheduleAt(raw string) (int64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}
	if ts, err := time.Parse(time.RFC3339, normalizeScheduleAtString(trimmed)); err == nil {
		return ts.UTC().UnixMilli(), true
	}
	return 0, false
}

func normalizeScheduleAtString(raw string) string {
	if strings.HasSuffix(raw, "Z") || strings.Contains(raw, "+") || strings.LastIndex(raw, "-") > 9 {
		return raw
	}
	if strings.Contains(raw, "T") {
		return raw + "Z"
	}
	if len(raw) == len("2006-01-02") {
		return raw + "T00:00:00Z"
	}
	return raw
}

func buildTickRunKey(revision int, kind string, scheduledForMs int64) string {
	return fmt.Sprintf("rev%d:%s:%d", revision, strings.TrimSpace(kind), scheduledForMs)
}

func shortTickKind(kind string) string {
	switch kind {
	case scheduleTickKindCronPlan, scheduleTickKindHeartbeatPlan:
		return "plan"
	default:
		return "run"
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
