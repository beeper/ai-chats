package cron

import (
	"strings"
)

type normalizeOptions struct {
	applyDefaults bool
}

func normalizeCronJobInput(raw CronJobCreate, opts normalizeOptions) CronJobCreate {
	next := raw

	if opts.applyDefaults {
		if next.Enabled == nil {
			enabled := true
			next.Enabled = &enabled
		}
		if next.WakeMode == "" {
			next.WakeMode = CronWakeNextHeartbeat
		}
		if next.SessionTarget == "" {
			kind := normalizeString(next.Payload.Kind)
			if kind == "systemevent" {
				next.SessionTarget = CronSessionMain
			} else if kind == "agentturn" {
				next.SessionTarget = CronSessionIsolated
			}
		}
		if next.DeleteAfterRun == nil && strings.EqualFold(strings.TrimSpace(next.Schedule.Kind), "at") {
			deleteAfter := true
			next.DeleteAfterRun = &deleteAfter
		}
		if next.Delivery == nil {
			payloadKind := normalizeString(next.Payload.Kind)
			if next.SessionTarget == CronSessionIsolated || (next.SessionTarget == "" && payloadKind == "agentturn") {
				next.Delivery = &CronDelivery{Mode: CronDeliveryAnnounce}
			}
		}
	}

	return next
}

// NormalizeCronJobCreate applies OpenClaw-like defaults.
func NormalizeCronJobCreate(raw CronJobCreate) CronJobCreate {
	return normalizeCronJobInput(raw, normalizeOptions{applyDefaults: true})
}

// NormalizeCronJobPatch currently no-op; provided for parity.
func NormalizeCronJobPatch(raw CronJobPatch) CronJobPatch {
	return raw
}

// CoerceSchedule fills kind based on fields.
func CoerceSchedule(schedule CronSchedule) CronSchedule {
	next := schedule
	kind := strings.TrimSpace(schedule.Kind)
	if kind == "" {
		switch {
		case strings.TrimSpace(schedule.At) != "":
			next.Kind = "at"
		case schedule.EveryMs > 0:
			next.Kind = "every"
		case strings.TrimSpace(schedule.Expr) != "":
			next.Kind = "cron"
		}
	}
	return next
}

// CoerceScheduleFromInput supports at string parsing.
func CoerceScheduleFromInput(schedule CronSchedule, atRaw string) CronSchedule {
	next := schedule
	if strings.TrimSpace(next.At) == "" && strings.TrimSpace(atRaw) != "" {
		next.At = strings.TrimSpace(atRaw)
	}
	return CoerceSchedule(next)
}
