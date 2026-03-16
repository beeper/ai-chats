package ai

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	integrationcron "github.com/beeper/agentremote/pkg/integrations/cron"
)

type cronJob = integrationcron.Job

func (s *schedulerRuntime) CronStatus(ctx context.Context) (bool, string, int, *int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	store, err := s.loadCronStoreLocked(ctx)
	if err != nil {
		return false, "sqlite:aichats_cron_jobs", 0, nil, err
	}
	var next *int64
	for i := range store.Jobs {
		job := &store.Jobs[i]
		if !job.Job.Enabled || job.Job.State.NextRunAtMs == nil || *job.Job.State.NextRunAtMs <= 0 {
			continue
		}
		if next == nil || *job.Job.State.NextRunAtMs < *next {
			val := *job.Job.State.NextRunAtMs
			next = &val
		}
	}
	return true, "sqlite:aichats_cron_jobs", len(store.Jobs), next, nil
}

func (s *schedulerRuntime) CronList(ctx context.Context, includeDisabled bool) ([]integrationcron.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	store, err := s.loadCronStoreLocked(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]integrationcron.Job, 0, len(store.Jobs))
	for _, job := range store.Jobs {
		if !includeDisabled && !job.Job.Enabled {
			continue
		}
		out = append(out, job.Job)
	}
	slices.SortFunc(out, func(a, b integrationcron.Job) int {
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *schedulerRuntime) CronAdd(ctx context.Context, jobInput integrationcron.JobCreate) (integrationcron.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	nowMs := time.Now().UnixMilli()
	jobInput = integrationcron.NormalizeJobCreate(jobInput)
	if err := validateCronCreateForScheduler(&jobInput); err != nil {
		return integrationcron.Job{}, err
	}
	if result := integrationcron.ValidateSchedule(jobInput.Schedule); !result.Ok {
		return integrationcron.Job{}, errors.New(result.Message)
	}
	if result := integrationcron.ValidateScheduleTimestamp(jobInput.Schedule, nowMs); !result.Ok {
		return integrationcron.Job{}, errors.New(result.Message)
	}

	store, err := s.loadCronStoreLocked(ctx)
	if err != nil {
		return integrationcron.Job{}, err
	}

	job := integrationcron.Job{
		ID:             uuid.NewString(),
		AgentID:        normalizedCronAgentID(jobInput.AgentID),
		Name:           resolveCronJobName(jobInput),
		Description:    optionalCronDescription(jobInput.Description),
		Enabled:        cronCreateEnabled(jobInput.Enabled),
		DeleteAfterRun: cronDeleteAfterRun(jobInput),
		CreatedAtMs:    nowMs,
		UpdatedAtMs:    nowMs,
		Schedule:       jobInput.Schedule,
		Payload:        jobInput.Payload,
		Delivery:       normalizeCronDelivery(jobInput.Delivery),
	}
	record := scheduledCronJob{Job: job, Revision: 1}
	if err := s.ensureCronRoomLocked(ctx, &record); err != nil {
		return integrationcron.Job{}, err
	}
	s.scheduleCronRecordLocked(ctx, &record, nowMs, false)

	store.Jobs = append(store.Jobs, record)
	if err := s.saveCronStoreLocked(ctx, store); err != nil {
		return integrationcron.Job{}, err
	}
	return record.Job, nil
}

func (s *schedulerRuntime) CronUpdate(ctx context.Context, jobID string, patch integrationcron.JobPatch) (integrationcron.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	store, err := s.loadCronStoreLocked(ctx)
	if err != nil {
		return integrationcron.Job{}, err
	}
	idx := findScheduledCronJob(store.Jobs, jobID)
	if idx < 0 {
		return integrationcron.Job{}, fmt.Errorf("cron job not found: %s", strings.TrimSpace(jobID))
	}
	record := store.Jobs[idx]
	if err := validateCronPatchForScheduler(&patch); err != nil {
		return integrationcron.Job{}, err
	}
	updated, err := applyScheduledCronPatch(record, patch, time.Now().UnixMilli())
	if err != nil {
		return integrationcron.Job{}, err
	}
	if err := s.cancelPendingDelayLocked(ctx, record.PendingDelayID); err != nil {
		s.client.log.Warn().Err(err).Str("job_id", record.Job.ID).Msg("Failed to cancel pending cron delay during update")
	}
	record = updated
	if err := s.ensureCronRoomLocked(ctx, &record); err != nil {
		return integrationcron.Job{}, err
	}
	s.scheduleCronRecordLocked(ctx, &record, time.Now().UnixMilli(), false)
	store.Jobs[idx] = record
	if err := s.saveCronStoreLocked(ctx, store); err != nil {
		return integrationcron.Job{}, err
	}
	return record.Job, nil
}

func (s *schedulerRuntime) CronRemove(ctx context.Context, jobID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	store, err := s.loadCronStoreLocked(ctx)
	if err != nil {
		return false, err
	}
	idx := findScheduledCronJob(store.Jobs, jobID)
	if idx < 0 {
		return false, nil
	}
	record := store.Jobs[idx]
	if err := s.cancelPendingDelayLocked(ctx, record.PendingDelayID); err != nil {
		s.client.log.Warn().Err(err).Str("job_id", record.Job.ID).Msg("Failed to cancel pending cron delay during remove")
	}
	store.Jobs = append(store.Jobs[:idx], store.Jobs[idx+1:]...)
	if err := s.saveCronStoreLocked(ctx, store); err != nil {
		return false, err
	}
	return true, nil
}

func (s *schedulerRuntime) CronRun(ctx context.Context, jobID string) (bool, string, error) {
	s.mu.Lock()
	store, err := s.loadCronStoreLocked(ctx)
	if err != nil {
		s.mu.Unlock()
		return false, "", err
	}
	idx := findScheduledCronJob(store.Jobs, jobID)
	if idx < 0 {
		s.mu.Unlock()
		return false, "not-found", nil
	}
	record := store.Jobs[idx]
	if !record.Job.Enabled {
		s.mu.Unlock()
		return false, "disabled", nil
	}
	s.mu.Unlock()

	tick := ScheduleTickContent{
		Kind:           scheduleTickKindCronRun,
		EntityID:       record.Job.ID,
		Revision:       record.Revision,
		ScheduledForMs: time.Now().UnixMilli(),
		RunKey:         buildTickRunKey(record.Revision, "manual", time.Now().UnixMilli()),
		Reason:         "manual",
	}
	if err := s.handleCronRun(ctx, tick, true); err != nil {
		return false, "", err
	}
	return true, "", nil
}

func (s *schedulerRuntime) reconcileCronLocked(ctx context.Context) error {
	store, err := s.loadCronStoreLocked(ctx)
	if err != nil {
		return err
	}
	nowMs := time.Now().UnixMilli()
	for idx := range store.Jobs {
		record := &store.Jobs[idx]
		if record.Revision <= 0 {
			record.Revision = 1
		}
		if err := s.ensureCronRoomLocked(ctx, record); err != nil {
			return err
		}
		s.scheduleCronRecordLocked(ctx, record, nowMs, true)
	}
	return s.saveCronStoreLocked(ctx, store)
}

func (s *schedulerRuntime) handleCronPlan(ctx context.Context, tick ScheduleTickContent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	store, err := s.loadCronStoreLocked(ctx)
	if err != nil {
		return err
	}
	idx := findScheduledCronJob(store.Jobs, tick.EntityID)
	if idx < 0 {
		return nil
	}
	record := &store.Jobs[idx]
	if !record.Job.Enabled || tick.Revision != record.Revision || containsRunKey(record.ProcessedRunKeys, tick.RunKey) {
		return nil
	}
	record.PendingDelayID = ""
	record.PendingDelayKind = ""
	record.PendingRunKey = ""
	record.ProcessedRunKeys = appendRunKey(record.ProcessedRunKeys, tick.RunKey)
	s.scheduleCronRecordLocked(ctx, record, time.Now().UnixMilli(), false)
	return s.saveCronStoreLocked(ctx, store)
}

func (s *schedulerRuntime) handleCronRun(ctx context.Context, tick ScheduleTickContent, manual bool) error {
	s.mu.Lock()
	store, err := s.loadCronStoreLocked(ctx)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	idx := findScheduledCronJob(store.Jobs, tick.EntityID)
	if idx < 0 {
		s.mu.Unlock()
		return nil
	}
	record := store.Jobs[idx]
	if !record.Job.Enabled || tick.Revision != record.Revision || containsRunKey(record.ProcessedRunKeys, tick.RunKey) {
		s.mu.Unlock()
		return nil
	}
	nowMs := time.Now().UnixMilli()
	record.Job.State.RunningAtMs = &nowMs
	if !manual {
		record.PendingDelayID = ""
		record.PendingDelayKind = ""
		record.PendingRunKey = ""
		s.scheduleNextCronAfterRunLocked(ctx, &record, tick.ScheduledForMs, nowMs)
	}
	store.Jobs[idx] = record
	if err := s.saveCronStoreLocked(ctx, store); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	status, errText, preview := s.executeCronJob(ctx, &record)

	s.mu.Lock()
	defer s.mu.Unlock()
	store, err = s.loadCronStoreLocked(ctx)
	if err != nil {
		return err
	}
	idx = findScheduledCronJob(store.Jobs, tick.EntityID)
	if idx < 0 {
		return nil
	}
	record = store.Jobs[idx]
	if !record.Job.Enabled || tick.Revision != record.Revision || containsRunKey(record.ProcessedRunKeys, tick.RunKey) {
		return nil
	}
	finishedAt := time.Now().UnixMilli()
	record.Job.State.RunningAtMs = nil
	record.Job.State.LastRunAtMs = &finishedAt
	record.Job.State.LastStatus = status
	record.Job.State.LastError = errText
	record.Job.State.LastDurationMs = nil
	record.LastOutputPreview = preview
	record.ProcessedRunKeys = appendRunKey(record.ProcessedRunKeys, tick.RunKey)
	record.Job.UpdatedAtMs = finishedAt
	if record.Job.DeleteAfterRun {
		if record.PendingDelayID != "" {
			if err := s.cancelPendingDelayLocked(ctx, record.PendingDelayID); err != nil {
				s.client.log.Warn().Err(err).Str("job_id", record.Job.ID).Msg("Failed to cancel pending cron delay during delete-after-run cleanup")
			}
		}
		record.Job.Enabled = false
		record.Job.State.NextRunAtMs = nil
		record.PendingDelayID = ""
		record.PendingDelayKind = ""
		record.PendingRunKey = ""
	}
	store.Jobs[idx] = record
	return s.saveCronStoreLocked(ctx, store)
}

func (s *schedulerRuntime) executeCronJob(ctx context.Context, record *scheduledCronJob) (string, string, string) {
	if s == nil || s.client == nil || record == nil {
		return "error", "missing scheduler", ""
	}
	portal := s.client.portalByRoomID(ctx, id.RoomID(record.RoomID))
	if portal == nil || portal.MXID == "" {
		return "error", "cron room not found", ""
	}
	meta := clonePortalMetadata(portalMeta(portal))
	if meta == nil {
		meta = &PortalMetadata{}
	}
	if portal.OtherUserID == "" {
		portal.OtherUserID = s.client.agentUserID(normalizedCronAgentID(&record.Job.AgentID))
	}
	meta.ResolvedTarget = resolveTargetFromGhostID(portal.OtherUserID)
	if model := strings.TrimSpace(record.Job.Payload.Model); model != "" {
		meta.RuntimeModelOverride = ResolveAlias(model)
	}
	if record.Job.Delivery != nil && record.Job.Delivery.Mode == integrationcron.DeliveryAnnounce {
		meta.DisabledTools = appendMissingDisabledTool(meta.DisabledTools, "message")
	}

	timeoutSeconds := resolveScheduledCronTimeoutSeconds(s.client, record.Job.Payload.TimeoutSeconds)
	runCtx, cancel := context.WithTimeout(s.client.backgroundContext(ctx), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	userTimezone, _ := s.client.resolveUserTimezone()
	message := integrationcron.BuildCronMessage(record.Job.ID, record.Job.Name, record.Job.Payload.Message, userTimezone)
	if record.Job.Payload.AllowUnsafeExternal == nil || !*record.Job.Payload.AllowUnsafeExternal {
		message = integrationcron.WrapSafeExternalPrompt(message)
	}
	lastID, lastTS := s.client.lastAssistantMessageInfo(runCtx, portal)
	if _, _, err := s.client.dispatchInternalMessage(runCtx, portal, meta, message, defaultScheduleEventSource, false); err != nil {
		return "error", err.Error(), ""
	}

	msg, found := s.client.waitForNewAssistantMessage(runCtx, portal, lastID, lastTS)
	if !found || msg == nil {
		return "error", "timed out waiting for cron response", ""
	}
	body := ""
	if meta := messageMeta(msg); meta != nil {
		body = strings.TrimSpace(meta.Body)
	}
	if body == "" {
		body = strings.TrimSpace(msg.MXID.String())
	}
	preview := truncateSchedulePreview(body)
	if record.Job.Delivery != nil && record.Job.Delivery.Mode == integrationcron.DeliveryAnnounce {
		target := s.resolveCronDeliveryTarget(record.Job.AgentID, record.Job.Delivery)
		portal, ok := target.Portal.(*bridgev2.Portal)
		if !ok || portal == nil || strings.TrimSpace(target.RoomID) == "" {
			return "skipped", "delivery target unavailable", preview
		}
		if err := s.client.sendPlainAssistantMessage(runCtx, portal, body); err != nil {
			return "error", err.Error(), preview
		}
	}
	return "success", "", preview
}

func (s *schedulerRuntime) resolveCronDeliveryTarget(agentID string, delivery *integrationcron.Delivery) integrationcron.DeliveryTarget {
	return integrationcron.ResolveCronDeliveryTarget(agentID, delivery, integrationcron.DeliveryResolverDeps{
		ResolveLastTarget: func(agentID string) (channel string, target string, ok bool) {
			ref, mainKey := s.client.resolveHeartbeatMainSessionRef(agentID)
			entry, found := s.client.getSessionEntry(context.Background(), ref, mainKey)
			if !found {
				return "", "", false
			}
			return entry.LastChannel, entry.LastTo, true
		},
		IsStaleTarget: func(roomID string, agentID string) bool {
			portal := s.client.portalByRoomID(context.Background(), id.RoomID(roomID))
			if portal == nil {
				return true
			}
			meta := portalMeta(portal)
			return meta != nil && normalizeAgentID(resolveAgentID(meta)) != normalizeAgentID(agentID)
		},
		LastActiveRoomID: func(agentID string) string {
			if portal := s.client.lastActivePortal(agentID); portal != nil && portal.MXID != "" {
				return portal.MXID.String()
			}
			return ""
		},
		DefaultChatRoomID: func() string {
			if portal := s.client.defaultChatPortal(); portal != nil && portal.MXID != "" {
				return portal.MXID.String()
			}
			return ""
		},
		ResolvePortalByRoom: func(roomID string) any {
			return s.client.portalByRoomID(context.Background(), id.RoomID(roomID))
		},
		IsLoggedIn: s.client.IsLoggedIn,
	})
}

func (s *schedulerRuntime) scheduleCronRecordLocked(ctx context.Context, record *scheduledCronJob, nowMs int64, validateExisting bool) {
	if record == nil {
		return
	}
	due := computeInitialCronDue(record.Job, nowMs)
	if due == nil || !record.Job.Enabled {
		record.Job.State.NextRunAtMs = nil
		record.PendingDelayID = ""
		record.PendingDelayKind = ""
		record.PendingRunKey = ""
		return
	}
	if validateExisting && record.PendingDelayID != "" {
		exists, err := s.delayedEventExistsLocked(ctx, record.PendingDelayID)
		if err != nil {
			s.client.log.Warn().Err(err).Str("job_id", record.Job.ID).Msg("Failed to validate existing cron delay")
			record.Job.State.LastStatus = "error"
			record.Job.State.LastError = err.Error()
			return
		}
		if exists {
			record.Job.State.NextRunAtMs = due
			return
		}
	}
	if record.PendingDelayID != "" {
		_ = s.cancelPendingDelayLocked(ctx, record.PendingDelayID)
	}
	s.scheduleCronDueLocked(ctx, record, *due)
}

func (s *schedulerRuntime) scheduleCronDueLocked(ctx context.Context, record *scheduledCronJob, dueAtMs int64) {
	if record == nil {
		return
	}
	nowMs := time.Now().UnixMilli()
	runAtMs := dueAtMs
	kind := scheduleTickKindCronRun
	if dueAtMs-nowMs > int64(schedulePlannerHorizon/time.Millisecond) {
		runAtMs = nowMs + int64(schedulePlannerHorizon/time.Millisecond)
		kind = scheduleTickKindCronPlan
	}
	resp, err := s.scheduleTickLocked(ctx, id.RoomID(record.RoomID), ScheduleTickContent{
		Kind:           kind,
		EntityID:       record.Job.ID,
		Revision:       record.Revision,
		ScheduledForMs: runAtMs,
		RunKey:         buildTickRunKey(record.Revision, shortTickKind(kind), runAtMs),
		Reason:         "interval",
	}, time.Duration(max64(runAtMs-nowMs, scheduleImmediateDelay.Milliseconds()))*time.Millisecond)
	if err != nil {
		s.client.log.Warn().Err(err).Str("job_id", record.Job.ID).Msg("Failed to schedule cron tick")
		record.Job.State.LastStatus = "error"
		record.Job.State.LastError = err.Error()
		return
	}
	record.Job.State.NextRunAtMs = &dueAtMs
	record.PendingDelayID = string(resp.UnstableDelayID)
	record.PendingDelayKind = shortTickKind(kind)
	record.PendingRunKey = buildTickRunKey(record.Revision, shortTickKind(kind), runAtMs)
}

func (s *schedulerRuntime) scheduleNextCronAfterRunLocked(ctx context.Context, record *scheduledCronJob, scheduledForMs, nowMs int64) {
	if record == nil {
		return
	}
	next := computeNextCronAfterRun(record.Job, scheduledForMs, nowMs)
	if next == nil {
		record.Job.State.NextRunAtMs = nil
		return
	}
	s.scheduleCronDueLocked(ctx, record, *next)
}

func validateCronCreateForScheduler(input *integrationcron.JobCreate) error {
	if input == nil {
		return errors.New("cron job is required")
	}
	return validateCronPayload(&input.Payload)
}

func validateCronPatchForScheduler(patch *integrationcron.JobPatch) error {
	if patch == nil {
		return errors.New("cron patch is required")
	}
	if patch.Payload != nil {
		if kind := strings.TrimSpace(patch.Payload.Kind); kind != "" {
			if !strings.EqualFold(kind, "agentTurn") {
				return fmt.Errorf("unsupported cron payload kind: %s", kind)
			}
			patch.Payload.Kind = "agentTurn"
		}
		if patch.Payload.Message != nil {
			trimmed := strings.TrimSpace(*patch.Payload.Message)
			patch.Payload.Message = &trimmed
		}
		if patch.Payload.Model != nil {
			trimmed := strings.TrimSpace(*patch.Payload.Model)
			patch.Payload.Model = &trimmed
		}
		if patch.Payload.Thinking != nil {
			trimmed := strings.TrimSpace(*patch.Payload.Thinking)
			patch.Payload.Thinking = &trimmed
		}
	}
	return nil
}

func validateCronPayload(payload *integrationcron.Payload) error {
	if payload == nil {
		return errors.New("payload is required")
	}
	if !strings.EqualFold(strings.TrimSpace(payload.Kind), "agentTurn") {
		return fmt.Errorf("unsupported cron payload kind: %s", strings.TrimSpace(payload.Kind))
	}
	payload.Kind = "agentTurn"
	payload.Message = strings.TrimSpace(payload.Message)
	if payload.Message == "" {
		return errors.New("payload.message is required")
	}
	return nil
}

func normalizeCronDelivery(delivery *integrationcron.Delivery) *integrationcron.Delivery {
	if delivery == nil {
		return &integrationcron.Delivery{Mode: integrationcron.DeliveryAnnounce}
	}
	mode := delivery.Mode
	if strings.TrimSpace(string(mode)) == "" {
		mode = integrationcron.DeliveryAnnounce
	}
	copyDelivery := *delivery
	copyDelivery.Mode = mode
	return &copyDelivery
}

func resolveCronJobName(input integrationcron.JobCreate) string {
	name := strings.TrimSpace(input.Name)
	if name != "" {
		return name
	}
	if strings.TrimSpace(input.Payload.Message) != "" {
		return truncateSchedulePreview(strings.TrimSpace(input.Payload.Message))
	}
	switch strings.ToLower(strings.TrimSpace(input.Schedule.Kind)) {
	case "cron":
		return "Cron job"
	case "every":
		return "Recurring job"
	default:
		return "Scheduled job"
	}
}

func optionalCronDescription(raw *string) string {
	if raw == nil {
		return ""
	}
	return strings.TrimSpace(*raw)
}

func cronCreateEnabled(raw *bool) bool {
	if raw == nil {
		return true
	}
	return *raw
}

func cronDeleteAfterRun(input integrationcron.JobCreate) bool {
	if input.DeleteAfterRun != nil {
		return *input.DeleteAfterRun
	}
	return strings.EqualFold(strings.TrimSpace(input.Schedule.Kind), "at")
}

func normalizedCronAgentID(raw *string) string {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return normalizeAgentID("default")
	}
	return normalizeAgentID(*raw)
}

func applyScheduledCronPatch(record scheduledCronJob, patch integrationcron.JobPatch, nowMs int64) (scheduledCronJob, error) {
	if patch.AgentID != nil {
		agentID := normalizeAgentID(strings.TrimSpace(*patch.AgentID))
		if agentID == "" {
			agentID = "default"
		}
		record.Job.AgentID = agentID
	}
	if patch.Name != nil {
		record.Job.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Description != nil {
		record.Job.Description = strings.TrimSpace(*patch.Description)
	}
	if patch.Enabled != nil {
		record.Job.Enabled = *patch.Enabled
	}
	if patch.DeleteAfterRun != nil {
		record.Job.DeleteAfterRun = *patch.DeleteAfterRun
	}
	if patch.Schedule != nil {
		if result := integrationcron.ValidateSchedule(*patch.Schedule); !result.Ok {
			return record, errors.New(result.Message)
		}
		if result := integrationcron.ValidateScheduleTimestamp(*patch.Schedule, nowMs); !result.Ok {
			return record, errors.New(result.Message)
		}
		record.Job.Schedule = *patch.Schedule
	}
	if patch.Payload != nil {
		if strings.TrimSpace(patch.Payload.Kind) != "" {
			record.Job.Payload.Kind = patch.Payload.Kind
		}
		if patch.Payload.Message != nil {
			record.Job.Payload.Message = strings.TrimSpace(*patch.Payload.Message)
		}
		if patch.Payload.Model != nil {
			record.Job.Payload.Model = strings.TrimSpace(*patch.Payload.Model)
		}
		if patch.Payload.Thinking != nil {
			record.Job.Payload.Thinking = strings.TrimSpace(*patch.Payload.Thinking)
		}
		if patch.Payload.TimeoutSeconds != nil {
			record.Job.Payload.TimeoutSeconds = patch.Payload.TimeoutSeconds
		}
		if patch.Payload.AllowUnsafeExternal != nil {
			record.Job.Payload.AllowUnsafeExternal = patch.Payload.AllowUnsafeExternal
		}
		if err := validateCronPayload(&record.Job.Payload); err != nil {
			return record, err
		}
	}
	if patch.Delivery != nil {
		delivery := normalizeCronDelivery(record.Job.Delivery)
		if patch.Delivery.Mode != nil {
			delivery.Mode = *patch.Delivery.Mode
		}
		if patch.Delivery.Channel != nil {
			delivery.Channel = strings.TrimSpace(*patch.Delivery.Channel)
		}
		if patch.Delivery.To != nil {
			delivery.To = strings.TrimSpace(*patch.Delivery.To)
		}
		if patch.Delivery.BestEffort != nil {
			delivery.BestEffort = patch.Delivery.BestEffort
		}
		record.Job.Delivery = normalizeCronDelivery(delivery)
	}
	record.Job.UpdatedAtMs = nowMs
	record.Revision++
	return record, nil
}

func computeInitialCronDue(job integrationcron.Job, nowMs int64) *int64 {
	switch strings.ToLower(strings.TrimSpace(job.Schedule.Kind)) {
	case "at":
		runAtMs, ok := parseScheduleAt(job.Schedule.At)
		if !ok {
			return nil
		}
		if job.State.LastRunAtMs != nil && *job.State.LastRunAtMs > 0 {
			return nil
		}
		if runAtMs <= nowMs {
			val := nowMs
			return &val
		}
		return &runAtMs
	default:
		return integrationcron.ComputeNextRunAtMs(job.Schedule, nowMs)
	}
}

func computeNextCronAfterRun(job integrationcron.Job, scheduledForMs, nowMs int64) *int64 {
	switch strings.ToLower(strings.TrimSpace(job.Schedule.Kind)) {
	case "at":
		return nil
	default:
		return integrationcron.ComputeNextRunAtMs(job.Schedule, max64(scheduledForMs, nowMs))
	}
}

func findScheduledCronJob(jobs []scheduledCronJob, jobID string) int {
	trimmed := strings.TrimSpace(jobID)
	for idx := range jobs {
		if strings.TrimSpace(jobs[idx].Job.ID) == trimmed {
			return idx
		}
	}
	return -1
}
