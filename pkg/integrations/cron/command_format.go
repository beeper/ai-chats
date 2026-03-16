package cron

import (
	"fmt"
	"strings"
	"time"
)

func formatCronStatusText(enabled bool, backend string, jobCount int, nextRunAtMs *int64) string {
	next := "none"
	if nextRunAtMs != nil && *nextRunAtMs > 0 {
		next = fmt.Sprintf("%s (%d)", formatUnixMs(*nextRunAtMs), *nextRunAtMs)
	}
	return fmt.Sprintf("Cron: enabled=%v jobs=%d backend=%s next=%s", enabled, jobCount, strings.TrimSpace(backend), next)
}

func formatCronJobListText(jobs []Job) string {
	if len(jobs) == 0 {
		return "Cron jobs: (none)"
	}
	var b strings.Builder
	b.WriteString("Cron jobs:\n")
	for _, job := range jobs {
		id := cronShortID(job.ID)
		name := strings.TrimSpace(job.Name)
		sched := formatCronSchedule(job.Schedule)
		deliver := ""
		if job.Delivery != nil {
			to := strings.TrimSpace(job.Delivery.To)
			ch := strings.TrimSpace(job.Delivery.Channel)
			mode := strings.TrimSpace(string(job.Delivery.Mode))
			if mode == "" {
				mode = "none"
			}
			if ch == "" {
				ch = "matrix"
			}
			if to != "" {
				deliver = fmt.Sprintf(" delivery=%s:%s:%s", mode, ch, to)
			} else {
				deliver = fmt.Sprintf(" delivery=%s:%s", mode, ch)
			}
		}
		state := ""
		if job.State.RunningAtMs != nil && *job.State.RunningAtMs > 0 {
			state = fmt.Sprintf(" runningSince=%s", formatUnixMs(*job.State.RunningAtMs))
		} else if job.State.NextRunAtMs != nil && *job.State.NextRunAtMs > 0 {
			state = fmt.Sprintf(" next=%s", formatUnixMs(*job.State.NextRunAtMs))
		}
		status := strings.TrimSpace(job.State.LastStatus)
		if status != "" {
			state += " last=" + status
		}
		b.WriteString(fmt.Sprintf("- %s %s schedule=%s%s%s\n", id, name, sched, deliver, state))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatCronSchedule(s Schedule) string {
	switch strings.ToLower(strings.TrimSpace(s.Kind)) {
	case "every":
		return fmt.Sprintf("every %s", formatDurationMs(s.EveryMs))
	case "at":
		if strings.TrimSpace(s.At) != "" {
			return "at " + strings.TrimSpace(s.At)
		}
		return "at"
	case "cron":
		expr := strings.TrimSpace(s.Expr)
		if expr == "" {
			return "cron"
		}
		tz := strings.TrimSpace(s.TZ)
		if tz != "" {
			return "cron " + expr + " tz=" + tz
		}
		return "cron " + expr
	default:
		if strings.TrimSpace(s.Kind) != "" {
			return strings.TrimSpace(s.Kind)
		}
		return "unknown"
	}
}

func formatDurationMs(ms int64) string {
	if ms <= 0 {
		return "0ms"
	}
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	default:
		return d.String()
	}
}

func formatUnixMs(ms int64) string {
	if ms <= 0 {
		return "unknown-time"
	}
	return time.UnixMilli(ms).In(time.Local).Format("2006-01-02 15:04:05 MST")
}

func cronShortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
