package cron

import (
	"strings"
	"time"
)

// ComputeNextRunAtMs returns the next run time in unix ms.
func ComputeNextRunAtMs(schedule CronSchedule, nowMs int64) *int64 {
	kind := strings.TrimSpace(schedule.Kind)
	switch kind {
	case "at":
		atMs, ok := parseAbsoluteTimeMs(schedule.At)
		if !ok {
			return nil
		}
		if atMs > nowMs {
			return &atMs
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
		steps := (elapsed + everyMs - 1) / everyMs
		if steps < 1 {
			steps = 1
		}
		next := anchor + steps*everyMs
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
