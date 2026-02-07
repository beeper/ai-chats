package cron

import (
	"cmp"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"
)

const stuckRunMs int64 = 2 * 60 * 60 * 1000

func createJob(nowMs int64, input CronJobCreate) (CronJob, error) {
	normalizedName, err := normalizeRequiredName(input.Name)
	if err != nil {
		return CronJob{}, err
	}
	enabled := false
	if input.Enabled != nil {
		enabled = *input.Enabled
	} else {
		enabled = true
	}
	deleteAfter := false
	if input.DeleteAfterRun != nil {
		deleteAfter = *input.DeleteAfterRun
	} else if strings.EqualFold(strings.TrimSpace(input.Schedule.Kind), "at") {
		deleteAfter = true
	}
	wakeMode := input.WakeMode
	if wakeMode == "" {
		wakeMode = CronWakeNextHeartbeat
	}

	job := CronJob{
		ID:             randomID(),
		AgentID:        normalizeOptionalAgentID(input.AgentID),
		Name:           normalizedName,
		Description:    normalizeOptionalText(input.Description),
		Enabled:        enabled,
		DeleteAfterRun: deleteAfter,
		CreatedAtMs:    nowMs,
		UpdatedAtMs:    nowMs,
		Schedule:       input.Schedule,
		SessionTarget:  input.SessionTarget,
		WakeMode:       wakeMode,
		Payload:        input.Payload,
		Delivery:       input.Delivery,
		State:          CronJobState{},
	}
	if input.State != nil {
		job.State = *input.State
	}
	if job.SessionTarget == CronSessionMain {
		job.Delivery = nil
	}
	if err := assertSupportedJobSpec(job.SessionTarget, job.Payload); err != nil {
		return CronJob{}, err
	}
	if err := assertDeliverySupport(job.SessionTarget, job.Delivery); err != nil {
		return CronJob{}, err
	}
	job.State.NextRunAtMs = computeJobNextRunAtMs(job, nowMs)
	return job, nil
}

func applyJobPatch(job *CronJob, patch CronJobPatch) error {
	if job == nil {
		return errors.New("job required")
	}
	if patch.Name != nil {
		name, err := normalizeRequiredName(*patch.Name)
		if err != nil {
			return err
		}
		job.Name = name
	}
	if patch.Description != nil {
		job.Description = normalizeOptionalText(patch.Description)
	}
	if patch.Enabled != nil {
		job.Enabled = *patch.Enabled
	}
	if patch.DeleteAfterRun != nil {
		job.DeleteAfterRun = *patch.DeleteAfterRun
	}
	if patch.Schedule != nil {
		job.Schedule = *patch.Schedule
	}
	if patch.SessionTarget != nil {
		job.SessionTarget = *patch.SessionTarget
	}
	if patch.WakeMode != nil {
		job.WakeMode = *patch.WakeMode
	}
	if patch.Payload != nil {
		merged, err := mergeCronPayload(job.Payload, *patch.Payload)
		if err != nil {
			return err
		}
		job.Payload = merged
	}
	if patch.Delivery != nil {
		job.Delivery = mergeCronDelivery(job.Delivery, *patch.Delivery)
	}
	if job.SessionTarget == CronSessionMain {
		job.Delivery = nil
	}
	if patch.State != nil {
		job.State = mergeCronState(job.State, *patch.State)
	}
	if patch.AgentID != nil {
		job.AgentID = normalizeOptionalAgentID(patch.AgentID)
	}
	if err := assertSupportedJobSpec(job.SessionTarget, job.Payload); err != nil {
		return err
	}
	return assertDeliverySupport(job.SessionTarget, job.Delivery)
}

func mergeCronState(existing CronJobState, patch CronJobState) CronJobState {
	next := existing
	if patch.NextRunAtMs != nil {
		next.NextRunAtMs = patch.NextRunAtMs
	}
	if patch.RunningAtMs != nil {
		next.RunningAtMs = patch.RunningAtMs
	}
	if patch.LastRunAtMs != nil {
		next.LastRunAtMs = patch.LastRunAtMs
	}
	if patch.LastStatus != "" {
		next.LastStatus = patch.LastStatus
	}
	if patch.LastError != "" {
		next.LastError = patch.LastError
	}
	if patch.LastDurationMs != nil {
		next.LastDurationMs = patch.LastDurationMs
	}
	return next
}

func mergeCronPayload(existing CronPayload, patch CronPayloadPatch) (CronPayload, error) {
	if patch.Kind != "" && !strings.EqualFold(patch.Kind, existing.Kind) {
		return buildPayloadFromPatch(patch)
	}
	kind := strings.ToLower(existing.Kind)
	if kind == "systemevent" {
		text := existing.Text
		if patch.Text != nil {
			text = *patch.Text
		}
		return CronPayload{Kind: "systemEvent", Text: text}, nil
	}
	next := existing
	if patch.Message != nil {
		next.Message = *patch.Message
	}
	if patch.Model != nil {
		next.Model = *patch.Model
	}
	if patch.Thinking != nil {
		next.Thinking = *patch.Thinking
	}
	if patch.TimeoutSeconds != nil {
		next.TimeoutSeconds = patch.TimeoutSeconds
	}
	if patch.AllowUnsafeExternal != nil {
		next.AllowUnsafeExternal = patch.AllowUnsafeExternal
	}
	return next, nil
}

func buildPayloadFromPatch(patch CronPayloadPatch) (CronPayload, error) {
	kind := strings.ToLower(patch.Kind)
	if kind == "systemevent" {
		text := ""
		if patch.Text != nil {
			text = *patch.Text
		}
		if strings.TrimSpace(text) == "" {
			return CronPayload{}, errors.New("cron.update payload.kind=systemEvent requires text")
		}
		return CronPayload{Kind: "systemEvent", Text: text}, nil
	}
	msg := ""
	if patch.Message != nil {
		msg = *patch.Message
	}
	if strings.TrimSpace(msg) == "" {
		return CronPayload{}, errors.New("cron.update payload.kind=agentTurn requires message")
	}
	return CronPayload{
		Kind:                "agentTurn",
		Message:             msg,
		Model:               derefString(patch.Model),
		Thinking:            derefString(patch.Thinking),
		TimeoutSeconds:      patch.TimeoutSeconds,
		AllowUnsafeExternal: patch.AllowUnsafeExternal,
	}, nil
}

func mergeCronDelivery(existing *CronDelivery, patch CronDeliveryPatch) *CronDelivery {
	if existing == nil {
		existing = &CronDelivery{Mode: CronDeliveryNone}
	}
	next := *existing
	if patch.Mode != nil {
		if strings.TrimSpace(string(*patch.Mode)) != "" {
			next.Mode = *patch.Mode
		}
	}
	if patch.Channel != nil {
		trimmed := strings.TrimSpace(*patch.Channel)
		next.Channel = trimmed
	}
	if patch.To != nil {
		trimmed := strings.TrimSpace(*patch.To)
		next.To = trimmed
	}
	if patch.BestEffort != nil {
		next.BestEffort = patch.BestEffort
	}
	return &next
}

func derefString(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func assertSupportedJobSpec(target CronSessionTarget, payload CronPayload) error {
	if target == CronSessionMain && !strings.EqualFold(payload.Kind, "systemEvent") {
		return errors.New("main cron jobs require payload.kind=systemEvent")
	}
	if target == CronSessionIsolated && !strings.EqualFold(payload.Kind, "agentTurn") {
		return errors.New("isolated cron jobs require payload.kind=agentTurn")
	}
	return nil
}

func assertDeliverySupport(target CronSessionTarget, delivery *CronDelivery) error {
	if delivery != nil && target != CronSessionIsolated {
		return errors.New("cron delivery config is only supported for sessionTarget=isolated")
	}
	return nil
}

func computeJobNextRunAtMs(job CronJob, nowMs int64) *int64 {
	if !job.Enabled {
		return nil
	}
	if strings.EqualFold(job.Schedule.Kind, "at") {
		if job.State.LastStatus == "ok" && job.State.LastRunAtMs != nil {
			return nil
		}
		atMs, ok := parseAbsoluteTimeMs(job.Schedule.At)
		if !ok || atMs <= 0 {
			return nil
		}
		return &atMs
	}
	return ComputeNextRunAtMs(job.Schedule, nowMs)
}

func recomputeNextRuns(store *CronStoreFile, nowMs int64, log Logger) {
	if store == nil {
		return
	}
	for idx := range store.Jobs {
		job := store.Jobs[idx]
		if !job.Enabled {
			job.State.NextRunAtMs = nil
			job.State.RunningAtMs = nil
			store.Jobs[idx] = job
			continue
		}
		if job.State.RunningAtMs != nil && nowMs-*job.State.RunningAtMs > stuckRunMs {
			if log != nil {
				log.Warn("cron: clearing stuck running marker", map[string]any{"jobId": job.ID, "runningAtMs": *job.State.RunningAtMs})
			}
			job.State.RunningAtMs = nil
		}
		job.State.NextRunAtMs = computeJobNextRunAtMs(job, nowMs)
		store.Jobs[idx] = job
	}
}

func nextWakeAtMs(store *CronStoreFile) *int64 {
	if store == nil {
		return nil
	}
	var next *int64
	for _, job := range store.Jobs {
		if !job.Enabled || job.State.NextRunAtMs == nil {
			continue
		}
		if next == nil || *job.State.NextRunAtMs < *next {
			val := *job.State.NextRunAtMs
			next = &val
		}
	}
	return next
}

func isJobDue(job CronJob, nowMs int64, forced bool) bool {
	if forced {
		return true
	}
	return job.Enabled && job.State.NextRunAtMs != nil && nowMs >= *job.State.NextRunAtMs
}

func findJobIndex(jobs []CronJob, id string) int {
	return slices.IndexFunc(jobs, func(job CronJob) bool {
		return job.ID == id
	})
}

func sortJobs(jobs []CronJob) {
	const maxInt64 = int64(1<<63 - 1)
	slices.SortFunc(jobs, func(a, b CronJob) int {
		av := maxInt64
		bv := maxInt64
		if a.State.NextRunAtMs != nil {
			av = *a.State.NextRunAtMs
		}
		if b.State.NextRunAtMs != nil {
			bv = *b.State.NextRunAtMs
		}
		return cmp.Compare(av, bv)
	})
}

func randomID() string {
	return uuid.NewString()
}

// helpers defined in normalize.go and utils.go
