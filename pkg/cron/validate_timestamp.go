package cron

import (
	"fmt"
	"strings"
	"time"
)

const (
	oneMinuteMs int64 = 60 * 1000
	tenYearsMs  int64 = int64(10 * 365.25 * 24 * 60 * 60 * 1000)
)

// TimestampValidationResult mirrors OpenClaw timestamp validation output.
type TimestampValidationResult struct {
	Ok      bool
	Message string
}

// ValidateScheduleTimestamp validates "at" schedules.
// Rejects timestamps that are more than 1 minute in the past or 10 years in the future.
func ValidateScheduleTimestamp(schedule CronSchedule, nowMs int64) TimestampValidationResult {
	if strings.TrimSpace(schedule.Kind) != "at" {
		return TimestampValidationResult{Ok: true}
	}
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	atRaw := strings.TrimSpace(schedule.At)
	atMs, ok := parseAbsoluteTimeMs(atRaw)
	if !ok {
		return TimestampValidationResult{
			Ok:      false,
			Message: fmt.Sprintf("Invalid schedule.at: expected ISO-8601 timestamp (got %v)", schedule.At),
		}
	}
	diffMs := atMs - nowMs
	if diffMs < -oneMinuteMs {
		nowDate := time.UnixMilli(nowMs).UTC().Format("2006-01-02T15:04:05.000Z")
		atDate := time.UnixMilli(atMs).UTC().Format("2006-01-02T15:04:05.000Z")
		minutesAgo := int64(-diffMs / oneMinuteMs)
		return TimestampValidationResult{
			Ok: false,
			Message: fmt.Sprintf(
				"schedule.at is in the past: %s (%d minutes ago). Current time: %s",
				atDate,
				minutesAgo,
				nowDate,
			),
		}
	}
	if diffMs > tenYearsMs {
		atDate := time.UnixMilli(atMs).UTC().Format("2006-01-02T15:04:05.000Z")
		yearsAhead := int64(diffMs / int64(365.25*24*60*60*1000))
		return TimestampValidationResult{
			Ok: false,
			Message: fmt.Sprintf(
				"schedule.at is too far in the future: %s (%d years ahead). Maximum allowed: 10 years",
				atDate,
				yearsAhead,
			),
		}
	}
	return TimestampValidationResult{Ok: true}
}
