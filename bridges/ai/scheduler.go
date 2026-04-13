package ai

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	schedulePlannerHorizon        = 6*24*time.Hour + 23*time.Hour
	scheduleImmediateDelay        = 2 * time.Second
	scheduleHeartbeatCoalesce     = 60 * time.Second
	defaultCronTimeoutSeconds     = 600
	defaultScheduleEventSource    = "schedule"
	scheduleTickKindCronRun       = "cron-run"
	scheduleTickKindCronPlan      = "cron-plan"
	scheduleTickKindHeartbeatRun  = "heartbeat-run"
	scheduleTickKindHeartbeatPlan = "heartbeat-plan"
)

type schedulerRuntime struct {
	client *AIClient
	mu     sync.Mutex
	runCtx context.Context
	cancel context.CancelFunc
	timers map[string]*time.Timer
}

type scheduledCronStore struct {
	Jobs []scheduledCronJob `json:"jobs"`
}

type scheduledCronJob struct {
	Job               cronJob  `json:"job"`
	RoomID            string   `json:"roomId,omitempty"`
	Revision          int      `json:"revision,omitempty"`
	PendingRunKey     string   `json:"pendingRunKey,omitempty"`
	LastOutputPreview string   `json:"lastOutputPreview,omitempty"`
	ProcessedRunKeys  []string `json:"processedRunKeys,omitempty"`
}

type managedHeartbeatStore struct {
	Agents []managedHeartbeatState `json:"agents"`
}

type managedHeartbeatState struct {
	AgentID          string                      `json:"agentId"`
	Enabled          bool                        `json:"enabled"`
	IntervalMs       int64                       `json:"intervalMs"`
	ActiveHours      *HeartbeatActiveHoursConfig `json:"activeHours,omitempty"`
	RoomID           string                      `json:"roomId,omitempty"`
	Revision         int                         `json:"revision,omitempty"`
	NextRunAtMs      int64                       `json:"nextRunAtMs,omitempty"`
	PendingRunKey    string                      `json:"pendingRunKey,omitempty"`
	LastRunAtMs      int64                       `json:"lastRunAtMs,omitempty"`
	LastResult       string                      `json:"lastResult,omitempty"`
	LastError        string                      `json:"lastError,omitempty"`
	ProcessedRunKeys []string                    `json:"processedRunKeys,omitempty"`
}

func (state *managedHeartbeatState) applyConfig(agentID string, hb *HeartbeatConfig) {
	if state == nil {
		return
	}
	interval := resolveHeartbeatIntervalMs(nil, "", hb)
	if state.AgentID == "" {
		state.AgentID = normalizeAgentID(agentID)
	}
	if state.Revision <= 0 {
		state.Revision = 1
	}
	activeHours := cloneHeartbeatActiveHours(hb)
	hadConfig := state.IntervalMs > 0 || state.ActiveHours != nil || state.Enabled
	if state.IntervalMs != interval || !equalHeartbeatActiveHours(state.ActiveHours, activeHours) {
		state.IntervalMs = interval
		state.ActiveHours = activeHours
		if hadConfig {
			state.Revision++
		}
		state.PendingRunKey = ""
	}
	state.Enabled = interval > 0
}

func (state managedHeartbeatState) dueAt(client *AIClient, nowMs int64) int64 {
	if state.IntervalMs <= 0 {
		return 0
	}
	var dueAtMs int64
	if state.LastRunAtMs > 0 {
		dueAtMs = state.LastRunAtMs + state.IntervalMs
		return clampHeartbeatDueToActiveHours(client, state.ActiveHours, dueAtMs)
	}
	if client != nil {
		ref, sessionKey := client.resolveHeartbeatMainSessionRef(state.AgentID)
		if entry, ok := client.getSessionEntry(context.Background(), ref, sessionKey); ok && entry.LastHeartbeatSentAt > 0 {
			dueAtMs = entry.LastHeartbeatSentAt + state.IntervalMs
			return clampHeartbeatDueToActiveHours(client, state.ActiveHours, dueAtMs)
		}
	}
	dueAtMs = nowMs + state.IntervalMs
	return clampHeartbeatDueToActiveHours(client, state.ActiveHours, dueAtMs)
}

func (state managedHeartbeatState) acceptsTick(tick ScheduleTickContent) bool {
	return state.Enabled && state.Revision == tick.Revision && !containsRunKey(state.ProcessedRunKeys, tick.RunKey)
}

func (state *managedHeartbeatState) markRunProcessed(runKey string) {
	if state == nil {
		return
	}
	state.PendingRunKey = ""
	state.ProcessedRunKeys = appendRunKey(state.ProcessedRunKeys, runKey)
}

func (state *managedHeartbeatState) markRunScheduled(nextRunAtMs int64, runKey string) {
	if state == nil {
		return
	}
	state.NextRunAtMs = nextRunAtMs
	state.PendingRunKey = runKey
}

func (state *managedHeartbeatState) recordScheduleError(err error) {
	if state == nil || err == nil {
		return
	}
	state.LastResult = "error"
	state.LastError = err.Error()
}

func (state *managedHeartbeatState) recordRunResult(res heartbeatRunResult, finishedAtMs int64) bool {
	if state == nil {
		return false
	}
	state.LastResult = res.Status
	state.LastError = res.Reason
	if res.Status == "ran" || res.Status == "sent" {
		state.LastRunAtMs = finishedAtMs
		return true
	}
	return false
}

func newSchedulerRuntime(client *AIClient) *schedulerRuntime {
	return &schedulerRuntime{
		client: client,
		timers: make(map[string]*time.Timer),
	}
}

func (s *schedulerRuntime) Start(ctx context.Context) {
	if s == nil || s.client == nil {
		return
	}
	s.mu.Lock()
	s.ensureRuntimeContextLocked(s.client.backgroundContext(ctx))
	s.mu.Unlock()
	if err := s.reconcile(ctx); err != nil {
		s.client.log.Warn().Err(err).Msg("Failed to reconcile scheduler state")
	}
}

func (s *schedulerRuntime) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	for key, timer := range s.timers {
		timer.Stop()
		delete(s.timers, key)
	}
	s.runCtx = nil
}

func (s *schedulerRuntime) HandleScheduleTickContent(ctx context.Context, tick ScheduleTickContent) {
	if s == nil || s.client == nil {
		return
	}
	s.handleScheduleTickContent(ctx, tick)
}

func (s *schedulerRuntime) handleScheduleTickContent(ctx context.Context, tick ScheduleTickContent) {
	switch tick.Kind {
	case scheduleTickKindCronPlan:
		if err := s.handleCronPlan(ctx, tick); err != nil {
			s.client.log.Warn().Err(err).Str("job_id", tick.EntityID).Msg("Failed to handle cron planner tick")
		}
	case scheduleTickKindCronRun:
		if err := s.handleCronRun(ctx, tick, false); err != nil {
			s.client.log.Warn().Err(err).Str("job_id", tick.EntityID).Msg("Failed to handle cron run tick")
		}
	case scheduleTickKindHeartbeatPlan:
		if err := s.handleHeartbeatPlan(ctx, tick); err != nil {
			s.client.log.Warn().Err(err).Str("agent_id", tick.EntityID).Msg("Failed to handle heartbeat planner tick")
		}
	case scheduleTickKindHeartbeatRun:
		if err := s.handleHeartbeatRun(ctx, tick); err != nil {
			s.client.log.Warn().Err(err).Str("agent_id", tick.EntityID).Msg("Failed to handle heartbeat run tick")
		}
	default:
		s.client.log.Debug().Str("kind", tick.Kind).Msg("Ignoring unknown schedule tick kind")
	}
}

func (s *schedulerRuntime) ensureRuntimeContextLocked(base context.Context) {
	if s.runCtx != nil && s.runCtx.Err() == nil {
		return
	}
	if base == nil {
		base = context.Background()
	}
	s.runCtx, s.cancel = context.WithCancel(base)
}

func (s *schedulerRuntime) reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var reconcileErrs []error
	if err := s.reconcileCronLocked(ctx); err != nil {
		s.client.log.Warn().Err(err).Msg("Failed to reconcile cron state")
		reconcileErrs = append(reconcileErrs, err)
	}
	if err := s.reconcileHeartbeatLocked(ctx); err != nil {
		s.client.log.Warn().Err(err).Msg("Failed to reconcile managed heartbeat state")
		reconcileErrs = append(reconcileErrs, err)
	}
	return errors.Join(reconcileErrs...)
}
