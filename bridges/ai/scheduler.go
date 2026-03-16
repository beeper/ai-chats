package ai

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
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
}

type scheduledCronStore struct {
	Jobs []scheduledCronJob `json:"jobs"`
}

type scheduledCronJob struct {
	Job               cronJob  `json:"job"`
	RoomID            string   `json:"roomId,omitempty"`
	Revision          int      `json:"revision,omitempty"`
	PendingDelayID    string   `json:"pendingDelayId,omitempty"`
	PendingDelayKind  string   `json:"pendingDelayKind,omitempty"`
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
	PendingDelayID   string                      `json:"pendingDelayId,omitempty"`
	PendingDelayKind string                      `json:"pendingDelayKind,omitempty"`
	PendingRunKey    string                      `json:"pendingRunKey,omitempty"`
	LastRunAtMs      int64                       `json:"lastRunAtMs,omitempty"`
	LastResult       string                      `json:"lastResult,omitempty"`
	LastError        string                      `json:"lastError,omitempty"`
	ProcessedRunKeys []string                    `json:"processedRunKeys,omitempty"`
}

func newSchedulerRuntime(client *AIClient) *schedulerRuntime {
	return &schedulerRuntime{client: client}
}

func (s *schedulerRuntime) Start(ctx context.Context) {
	if s == nil || s.client == nil {
		return
	}
	if err := s.reconcile(ctx); err != nil {
		s.client.log.Warn().Err(err).Msg("Failed to reconcile scheduler state")
	}
}

func (s *schedulerRuntime) HandleScheduleTick(ctx context.Context, evt *event.Event, portal *bridgev2.Portal) {
	if s == nil || s.client == nil || evt == nil {
		return
	}
	var tick ScheduleTickContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &tick); err != nil {
		s.client.log.Warn().Err(err).Stringer("event_id", evt.ID).Msg("Failed to decode schedule tick")
		return
	}
	s.handleScheduleTickContent(ctx, tick, evt, portal)
}

func (s *schedulerRuntime) HandleScheduleTickContent(ctx context.Context, tick ScheduleTickContent, evt *event.Event, portal *bridgev2.Portal) {
	if s == nil || s.client == nil || evt == nil {
		return
	}
	s.handleScheduleTickContent(ctx, tick, evt, portal)
}

func (s *schedulerRuntime) handleScheduleTickContent(ctx context.Context, tick ScheduleTickContent, evt *event.Event, portal *bridgev2.Portal) {
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
