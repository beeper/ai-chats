package cron

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.mau.fi/util/ptr"
)

func (c *CronService) workerLoop() {
	defer c.workersWg.Done()
	for {
		select {
		case <-c.ctx.Done():
			return
		case task := <-c.taskCh:
			c.executeTask(task)
		}
	}
}

func (c *CronService) executeTask(task cronTask) {
	// Track in-flight and clear queued marker.
	if task.resp != nil || !task.forced {
		c.qmu.Lock()
		delete(c.queued, task.jobID)
		c.inFlight[task.jobID] = struct{}{}
		c.qmu.Unlock()
	}

	defer func() {
		c.qmu.Lock()
		delete(c.inFlight, task.jobID)
		c.qmu.Unlock()
		c.wakeScheduler()
	}()

	ran, reason, err := c.executeJob(task.jobID, task.forced)
	if task.resp != nil {
		task.resp <- cronTaskResult{ran: ran, reason: reason, err: err}
	}
}

func (c *CronService) executeJob(jobID string, forced bool) (bool, string, error) {
	now := c.deps.NowMs()

	// Phase 1: claim under store lock.
	var startedAt int64
	var snapshot CronJob
	err := c.withStoreLock(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()

		if err := c.ensureLoadedLocked(true); err != nil {
			return fmt.Errorf("load store for job %s: %w", jobID, err)
		}
		idx := findJobIndex(c.store.Jobs, jobID)
		if idx == -1 {
			return fmt.Errorf("unknown cron job id: %s", jobID)
		}
		job := c.store.Jobs[idx]
		if !job.Enabled {
			return errJobNotRunnable("disabled")
		}

		// Clear stuck running marker.
		if job.State.RunningAtMs != nil && now-*job.State.RunningAtMs > stuckRunMs {
			job.State.RunningAtMs = nil
		}
		if job.State.RunningAtMs != nil {
			return errJobNotRunnable("already-running")
		}
		if !forced && (job.State.NextRunAtMs == nil || now < *job.State.NextRunAtMs) {
			return errJobNotRunnable("not-due")
		}

		startedAt = now
		job.State.RunningAtMs = &startedAt
		job.State.LastError = ""
		c.store.Jobs[idx] = job
		c.emit(CronEvent{JobID: job.ID, Action: "started", RunAtMs: startedAt})
		c.log("info", "cron: job starting", map[string]any{
			"jobId":   job.ID,
			"name":    job.Name,
			"session": string(job.SessionTarget),
			"payload": job.Payload.Kind,
		})
		if err := c.persistLocked(); err != nil {
			c.log("warn", "cron: persist started marker failed", map[string]any{"jobId": job.ID, "error": err.Error()})
		}
		snapshot = job
		return nil
	})
	if err != nil {
		if unr, ok := asJobNotRunnable(err); ok {
			return false, unr.reason, nil
		}
		return false, "", err
	}

	// Phase 2: execute outside store lock under a hard timeout.
	// Derive from the service context so Stop() cancellation interrupts active jobs.
	timeout := c.resolveJobTimeout(snapshot)
	jobCtx, jobCancel := context.WithTimeout(c.ctx, timeout)
	defer jobCancel()

	statusVal, errVal, summaryVal, _ := c.runJob(jobCtx, snapshot)

	// Phase 3: finalize under store lock.
	var deleted bool
	finishErr := c.finalizeJob(jobID, startedAt, statusVal, errVal, summaryVal, &deleted)
	if finishErr != nil {
		if err == nil {
			err = finishErr
		} else {
			c.log("warn", "cron: finalize failed after run", map[string]any{"jobId": snapshot.ID, "error": finishErr.Error()})
		}
	}

	// Post summary back to main session for isolated jobs (best-effort).
	c.postIsolatedSummary(snapshot, deleted, statusVal, summaryVal)

	if err != nil {
		return true, "", err
	}
	return true, "", nil
}

func (c *CronService) finalizeJob(jobID string, startedAt int64, statusVal, errVal, summaryVal string, deleted *bool) error {
	return c.withStoreLock(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()

		if err := c.ensureLoadedLocked(true); err != nil {
			return fmt.Errorf("load store for finalize %s: %w", jobID, err)
		}
		idx := findJobIndex(c.store.Jobs, jobID)
		if idx == -1 {
			return nil
		}
		job := c.store.Jobs[idx]
		endedAt := c.deps.NowMs()
		durationMs := max(0, endedAt-startedAt)

		job.State.RunningAtMs = nil
		job.State.LastRunAtMs = &startedAt
		job.State.LastStatus = statusVal
		job.State.LastDurationMs = ptr.Ptr(durationMs)
		job.State.LastError = errVal
		job.UpdatedAtMs = endedAt

		shouldDelete := job.Schedule.Kind == "at" && statusVal == "ok" && job.DeleteAfterRun
		if shouldDelete {
			// Job will be removed; clear NextRunAtMs so the finished event does not
			// advertise a stale next-run time for a job that no longer exists.
			job.State.NextRunAtMs = nil
		} else {
			switch {
			case job.Schedule.Kind == "at" && statusVal == "ok":
				job.Enabled = false
				job.State.NextRunAtMs = nil
			case job.Enabled:
				job.State.NextRunAtMs = computeJobNextRunAtMs(job, endedAt)
			default:
				job.State.NextRunAtMs = nil
			}
		}

		c.emit(CronEvent{
			JobID:       job.ID,
			Action:      "finished",
			RunAtMs:     startedAt,
			DurationMs:  durationMs,
			Status:      statusVal,
			Error:       errVal,
			Summary:     summaryVal,
			NextRunAtMs: ptr.Val(job.State.NextRunAtMs),
		})
		c.log("info", "cron: job finished", map[string]any{
			"jobId":      job.ID,
			"name":       job.Name,
			"status":     statusVal,
			"error":      errVal,
			"durationMs": durationMs,
		})

		if shouldDelete {
			c.store.Jobs = slices.DeleteFunc(c.store.Jobs, func(existing CronJob) bool {
				return existing.ID == job.ID
			})
			c.emit(CronEvent{JobID: job.ID, Action: "removed"})
			*deleted = true
		} else {
			c.store.Jobs[idx] = job
		}
		if err := c.persistLocked(); err != nil {
			c.log("warn", "cron: persist after job finished", map[string]any{"jobId": job.ID, "error": err.Error()})
		}
		return nil
	})
}

func (c *CronService) postIsolatedSummary(snapshot CronJob, deleted bool, statusVal, summaryVal string) {
	if deleted || snapshot.SessionTarget != CronSessionIsolated {
		return
	}
	summaryText := strings.TrimSpace(summaryVal)
	if summaryText == "" || c.deps.EnqueueSystemEvent == nil {
		return
	}
	deliveryMode := CronDeliveryAnnounce
	if snapshot.Delivery != nil && strings.TrimSpace(string(snapshot.Delivery.Mode)) != "" {
		deliveryMode = snapshot.Delivery.Mode
	}
	if deliveryMode == CronDeliveryNone {
		return
	}

	label := "Cron: " + summaryText
	if statusVal != "ok" {
		label = fmt.Sprintf("Cron (%s): %s", statusVal, summaryText)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_ = c.deps.EnqueueSystemEvent(ctx, label, snapshot.AgentID)
	cancel()

	if snapshot.WakeMode == CronWakeNow && c.deps.RequestHeartbeatNow != nil {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
		c.deps.RequestHeartbeatNow(ctx2, "cron:"+snapshot.ID+":post")
		cancel2()
	}
}

func (c *CronService) runJob(ctx context.Context, job CronJob) (statusVal, errVal, summaryVal, outputVal string) {
	if job.SessionTarget == CronSessionMain {
		return c.runMainJob(ctx, job)
	}
	if normalizeString(job.Payload.Kind) != "agentturn" {
		return "skipped", "isolated job requires payload.kind=agentTurn", "", ""
	}
	if c.deps.RunIsolatedAgentJob == nil {
		return "error", "isolated cron jobs not supported", "", ""
	}
	status, summary, output, runErr := c.deps.RunIsolatedAgentJob(ctx, job, job.Payload.Message)
	if runErr != nil {
		return "error", runErr.Error(), summary, output
	}
	if status == "ok" || status == "skipped" {
		return status, "", summary, output
	}
	return "error", "cron job failed", summary, output
}

func (c *CronService) runMainJob(ctx context.Context, job CronJob) (statusVal, errVal, summaryVal, outputVal string) {
	text, reason := resolveJobPayloadTextForMain(job)
	if text == "" {
		return "skipped", reason, "", ""
	}
	if c.deps.EnqueueSystemEvent == nil {
		return "error", "enqueueSystemEvent not configured", "", ""
	}
	if err := c.deps.EnqueueSystemEvent(ctx, text, job.AgentID); err != nil {
		return "error", err.Error(), text, ""
	}

	if job.WakeMode == CronWakeNow && c.deps.RunHeartbeatOnce != nil {
		reason := "cron:" + job.ID
		maxWait := 2 * time.Minute
		waitStarted := time.Now()
		for {
			if ctx.Err() != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return "error", "cron job timed out", text, ""
				}
				return "error", ctx.Err().Error(), text, ""
			}
			res := c.deps.RunHeartbeatOnce(ctx, reason)
			if res.Status != "skipped" || res.Reason != "requests-in-flight" {
				switch res.Status {
				case "ran":
					return "ok", "", text, ""
				case "skipped":
					return "skipped", res.Reason, text, ""
				default:
					return "error", res.Reason, text, ""
				}
			}
			if time.Since(waitStarted) > maxWait {
				return "skipped", "timeout waiting for main lane to become idle", text, ""
			}
			time.Sleep(250 * time.Millisecond)
		}
	}

	if c.deps.RequestHeartbeatNow != nil {
		c.deps.RequestHeartbeatNow(ctx, "cron:"+job.ID)
	}
	return "ok", "", text, ""
}

func (c *CronService) resolveJobTimeout(job CronJob) time.Duration {
	if c.deps.ResolveJobTimeoutMs != nil {
		if ms := c.deps.ResolveJobTimeoutMs(job); ms > 0 {
			return clampDuration(time.Duration(ms) * time.Millisecond)
		}
	}
	timeout := 10 * time.Minute
	if job.SessionTarget == CronSessionIsolated && job.Payload.TimeoutSeconds != nil {
		switch seconds := *job.Payload.TimeoutSeconds; {
		case seconds == 0:
			timeout = 30 * 24 * time.Hour
		case seconds > 0:
			timeout = time.Duration(seconds) * time.Second
		}
	}
	return clampDuration(timeout)
}

func clampDuration(d time.Duration) time.Duration {
	const maxDuration = 30 * 24 * time.Hour
	if d < time.Second {
		return time.Second
	}
	if d > maxDuration {
		return maxDuration
	}
	return d
}

type jobNotRunnable struct{ reason string }

func (e jobNotRunnable) Error() string { return "cron: job not runnable: " + e.reason }

func errJobNotRunnable(reason string) error { return jobNotRunnable{reason: reason} }

func asJobNotRunnable(err error) (jobNotRunnable, bool) {
	var e jobNotRunnable
	if errors.As(err, &e) {
		return e, true
	}
	return jobNotRunnable{}, false
}
