package connector

import "time"

func clampHeartbeatDueToActiveHours(oc *AIClient, cfg *HeartbeatActiveHoursConfig, dueAtMs int64) int64 {
	if cfg == nil || dueAtMs <= 0 {
		return dueAtMs
	}
	startMin := parseActiveHoursTime(cfg.Start, false)
	endMin := parseActiveHoursTime(cfg.End, true)
	if startMin == nil || endMin == nil {
		return dueAtMs
	}
	loc := resolveActiveHoursTimezone(oc, cfg.Timezone)
	if loc == nil {
		return dueAtMs
	}
	due := time.UnixMilli(dueAtMs).In(loc)
	currentMin := due.Hour()*60 + due.Minute()
	if activeHoursContainsMinute(currentMin, *startMin, *endMin) {
		return dueAtMs
	}
	midnight := time.Date(due.Year(), due.Month(), due.Day(), 0, 0, 0, 0, loc)
	if *endMin > *startMin {
		if currentMin < *startMin {
			return midnight.Add(time.Duration(*startMin) * time.Minute).UnixMilli()
		}
		return midnight.Add(24*time.Hour + time.Duration(*startMin)*time.Minute).UnixMilli()
	}
	return midnight.Add(time.Duration(*startMin) * time.Minute).UnixMilli()
}

func activeHoursContainsMinute(currentMin int, startMin int, endMin int) bool {
	if endMin > startMin {
		return currentMin >= startMin && currentMin < endMin
	}
	return currentMin >= startMin || currentMin < endMin
}
