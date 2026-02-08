package connector

import (
	"fmt"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/cron"
)

func formatCronStatusText(enabled bool, storePath string, jobCount int, nextWakeAtMs *int64) string {
	next := "none"
	if nextWakeAtMs != nil && *nextWakeAtMs > 0 {
		next = fmt.Sprintf("%s (%d)", formatUnixMs(*nextWakeAtMs), *nextWakeAtMs)
	}
	return fmt.Sprintf("Cron: enabled=%v jobs=%d store=%s next=%s", enabled, jobCount, strings.TrimSpace(storePath), next)
}

func formatCronJobListText(jobs []cron.CronJob) string {
	if len(jobs) == 0 {
		return "Cron jobs: (none)"
	}
	var b strings.Builder
	b.WriteString("Cron jobs:\n")
	for _, job := range jobs {
		id := cronShortID(job.ID)
		name := strings.TrimSpace(job.Name)
		sched := formatCronSchedule(job.Schedule)
		target := "main"
		if job.SessionTarget != "" {
			target = string(job.SessionTarget)
		}
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
			state = state + " last=" + status
		}
		b.WriteString(fmt.Sprintf("- %s %s schedule=%s target=%s%s%s\n", id, name, sched, target, deliver, state))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatCronRunsText(jobID string, entries []cron.CronRunLogEntry) string {
	jobID = strings.TrimSpace(jobID)
	if len(entries) == 0 {
		if jobID != "" {
			return fmt.Sprintf("Cron runs (%s): (none)", cronShortID(jobID))
		}
		return "Cron runs: (none)"
	}
	var b strings.Builder
	label := jobID
	if label != "" {
		label = cronShortID(label)
	}
	b.WriteString(fmt.Sprintf("Cron runs (%s):\n", label))
	for _, e := range entries {
		ts := "unknown-time"
		if e.TS > 0 {
			ts = formatUnixMs(e.TS)
		}
		status := strings.TrimSpace(e.Status)
		if status == "" {
			status = "unknown"
		}
		action := strings.TrimSpace(e.Action)
		if action == "" {
			action = "unknown"
		}
		dur := ""
		if e.DurationMs > 0 {
			dur = " duration=" + formatDurationMs(e.DurationMs)
		}
		errText := strings.TrimSpace(e.Error)
		if errText != "" {
			errText = " error=" + errText
		}
		summary := strings.TrimSpace(e.Summary)
		if summary != "" {
			summary = " " + summary
		}
		b.WriteString(fmt.Sprintf("- %s action=%s status=%s%s%s%s\n", ts, action, status, dur, errText, summary))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatCronSchedule(s cron.CronSchedule) string {
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
	// Prefer short human strings for the common cases.
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int64(d/time.Second))
	}
	return d.String()
}

func formatUnixMs(ms int64) string {
	if ms <= 0 {
		return "unknown-time"
	}
	// Use local time for operator debugging (matches other status outputs).
	return time.UnixMilli(ms).In(time.Local).Format("2006-01-02 15:04:05 MST")
}

func cronShortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
