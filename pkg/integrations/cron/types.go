package cron

import (
	"context"

	croncore "github.com/beeper/ai-bridge/pkg/cron"
)

// Public aliases so hosts can depend on integrations/cron without importing pkg/cron.
type (
	Job                       = croncore.CronJob
	JobCreate                 = croncore.CronJobCreate
	JobPatch                  = croncore.CronJobPatch
	Schedule                  = croncore.CronSchedule
	RunLogEntry               = croncore.CronRunLogEntry
	Event                     = croncore.CronEvent
	Delivery                  = croncore.CronDelivery
	DeliveryMode              = croncore.CronDeliveryMode
	SessionTarget             = croncore.CronSessionTarget
	Payload                   = croncore.CronPayload
	WakeMode                  = croncore.CronWakeMode
	Service                   = croncore.CronService
	StoreLogBackend           = croncore.StoreBackend
	TimestampValidationResult = croncore.TimestampValidationResult
	HeartbeatRunResult        = croncore.HeartbeatRunResult
)

const (
	SessionIsolated   = croncore.CronSessionIsolated
	SessionMain       = croncore.CronSessionMain
	DeliveryNone      = croncore.CronDeliveryNone
	DeliveryAnnounce  = croncore.CronDeliveryAnnounce
	WakeNextHeartbeat = croncore.CronWakeNextHeartbeat
	WakeNow           = croncore.CronWakeNow
)

func NormalizeJobCreateRaw(raw map[string]any) (JobCreate, error) {
	return croncore.NormalizeCronJobCreateRaw(raw)
}

func NormalizeJobPatchRaw(raw map[string]any) (JobPatch, error) {
	return croncore.NormalizeCronJobPatchRaw(raw)
}

func ValidateSchedule(s Schedule) TimestampValidationResult {
	return croncore.ValidateSchedule(s)
}

func ValidateScheduleTimestamp(s Schedule, nowMs int64) TimestampValidationResult {
	return croncore.ValidateScheduleTimestamp(s, nowMs)
}

func ResolveRunLogPath(storePath, jobID string) string {
	return croncore.ResolveCronRunLogPath(storePath, jobID)
}

func ResolveRunLogDir(storePath string) string {
	return croncore.ResolveCronRunLogDir(storePath)
}

func ParseRunLogEntries(raw string, limit int, jobID string) []RunLogEntry {
	return croncore.ParseCronRunLogEntries(raw, limit, jobID)
}

func ReadRunLogEntries(ctx context.Context, backend StoreLogBackend, path string, limit int, jobID string) ([]RunLogEntry, error) {
	return croncore.ReadCronRunLogEntries(ctx, backend, path, limit, jobID)
}

func AppendRunLog(ctx context.Context, backend StoreLogBackend, path string, entry RunLogEntry, maxBytes int64, maxEntries int) error {
	return croncore.AppendCronRunLog(ctx, backend, path, entry, maxBytes, maxEntries)
}
