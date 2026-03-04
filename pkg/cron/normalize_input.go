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
