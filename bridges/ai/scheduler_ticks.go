package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *schedulerRuntime) scheduleTickLocked(ctx context.Context, timerKey string, content ScheduleTickContent, delay time.Duration) error {
	if s == nil || s.client == nil {
		return errors.New("scheduler not available")
	}
	if strings.TrimSpace(timerKey) == "" {
		return errors.New("timer key is required")
	}
	if delay < scheduleImmediateDelay {
		delay = scheduleImmediateDelay
	}
	s.ensureRuntimeContextLocked(s.client.backgroundContext(ctx))
	if s.runCtx == nil || s.runCtx.Err() != nil {
		return errors.New("scheduler runtime is not running")
	}
	s.cancelScheduledTickLocked(timerKey)
	tick := content
	s.timers[timerKey] = time.AfterFunc(delay, func() {
		s.fireScheduledTick(timerKey, tick)
	})
	return nil
}

func (s *schedulerRuntime) fireScheduledTick(timerKey string, tick ScheduleTickContent) {
	s.mu.Lock()
	if s.timers != nil {
		delete(s.timers, timerKey)
	}
	runCtx := s.runCtx
	s.mu.Unlock()
	if runCtx == nil || runCtx.Err() != nil {
		return
	}
	s.handleScheduleTickContent(runCtx, tick)
}

func (s *schedulerRuntime) hasScheduledTickLocked(timerKey string) bool {
	if strings.TrimSpace(timerKey) == "" || s.timers == nil {
		return false
	}
	_, ok := s.timers[timerKey]
	return ok
}

func (s *schedulerRuntime) cancelScheduledTickLocked(timerKey string) {
	if strings.TrimSpace(timerKey) == "" || s.timers == nil {
		return
	}
	timer, ok := s.timers[timerKey]
	if !ok {
		return
	}
	timer.Stop()
	delete(s.timers, timerKey)
}

func cronTimerKey(jobID string) string {
	return "cron:" + strings.TrimSpace(jobID)
}

func heartbeatTimerKey(agentID string) string {
	return "heartbeat:" + strings.TrimSpace(agentID)
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
