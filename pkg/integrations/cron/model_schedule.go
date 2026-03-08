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

func ComputeNextRunAtMs(schedule Schedule, nowMs int64) *int64 {
	kind := strings.TrimSpace(schedule.Kind)
	switch kind {
	case "at":
		runAtMs, ok := parseAbsoluteTimeMs(schedule.At)
		if !ok {
			return nil
		}
		if runAtMs > nowMs {
			return &runAtMs
		}
		return nil
	case "every":
		everyMs := schedule.EveryMs
		if everyMs < 1 {
			everyMs = 1
		}
		anchor := int64(0)
		if schedule.AnchorMs != nil {
			anchor = *schedule.AnchorMs
		} else {
			anchor = nowMs
		}
		if anchor < 0 {
			anchor = 0
		}
		if nowMs < anchor {
			return &anchor
		}
		elapsed := nowMs - anchor
		steps := elapsed/everyMs + 1
		next := anchor + steps*everyMs
		if next <= nowMs {
			next += everyMs
		}
		return &next
	case "cron":
		expr := strings.TrimSpace(schedule.Expr)
		if expr == "" {
			return nil
		}
		location := time.UTC
		if tz := strings.TrimSpace(schedule.TZ); tz != "" {
			if loc, err := time.LoadLocation(tz); err == nil {
				location = loc
			}
		}
		sched, err := cronParser.Parse(expr)
		if err != nil {
			return nil
		}
		next := sched.Next(time.UnixMilli(nowMs).In(location))
		if next.IsZero() {
			return nil
		}
		nextMs := next.UTC().UnixMilli()
		return &nextMs
	default:
		return nil
	}
}

func ValidateSchedule(schedule Schedule) TimestampValidationResult {
	kind := strings.TrimSpace(schedule.Kind)
	if tz := strings.TrimSpace(schedule.TZ); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return TimestampValidationResult{
				Ok:      false,
				Message: fmt.Sprintf("Invalid schedule.tz: %q is not a valid IANA timezone (e.g. America/New_York, Europe/London, UTC)", tz),
			}
		}
	}
	if kind == "cron" {
		expr := strings.TrimSpace(schedule.Expr)
		if expr == "" {
			return TimestampValidationResult{
				Ok:      false,
				Message: "schedule.expr is required for kind=cron",
			}
		}
		if _, err := cronParser.Parse(expr); err != nil {
			return TimestampValidationResult{
				Ok:      false,
				Message: fmt.Sprintf("Invalid schedule.expr: %s", err.Error()),
			}
		}
	}
	if kind == "every" {
		if schedule.EveryMs <= 0 {
			return TimestampValidationResult{
				Ok:      false,
				Message: "schedule.everyMs must be greater than 0 for kind=every",
			}
		}
		return TimestampValidationResult{Ok: true}
	}
	if kind == "at" {
		return TimestampValidationResult{Ok: true}
	}
	if kind == "" {
		return TimestampValidationResult{
			Ok:      false,
			Message: "schedule.kind is required",
		}
	}
	return TimestampValidationResult{
		Ok:      false,
		Message: fmt.Sprintf("unsupported schedule.kind %q", kind),
	}
}

func ValidateScheduleTimestamp(schedule Schedule, nowMs int64) TimestampValidationResult {
	if strings.TrimSpace(schedule.Kind) != "at" {
		return TimestampValidationResult{Ok: true}
	}
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	atRaw := strings.TrimSpace(schedule.At)
	runAtMs, ok := parseAbsoluteTimeMs(atRaw)
	if !ok {
		return TimestampValidationResult{
			Ok:      false,
			Message: fmt.Sprintf("Invalid schedule.at: expected ISO-8601 timestamp (got %v)", schedule.At),
		}
	}
	diffMs := runAtMs - nowMs
	if diffMs < -oneMinuteMs {
		nowDate := time.UnixMilli(nowMs).UTC().Format("2006-01-02T15:04:05.000Z")
		atDate := time.UnixMilli(runAtMs).UTC().Format("2006-01-02T15:04:05.000Z")
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
		atDate := time.UnixMilli(runAtMs).UTC().Format("2006-01-02T15:04:05.000Z")
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
