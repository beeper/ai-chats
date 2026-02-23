package cron

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/ptr"
)

// Logger matches OpenClaw logger shape.
type Logger interface {
	Debug(msg string, fields ...any)
	Info(msg string, fields ...any)
	Warn(msg string, fields ...any)
	Error(msg string, fields ...any)
}

// HeartbeatRunResult mirrors OpenClaw heartbeat results.
type HeartbeatRunResult struct {
	Status string
	Reason string
}

// CronEvent is emitted on job changes.
type CronEvent struct {
	JobID       string
	Action      string
	RunAtMs     int64
	DurationMs  int64
	Status      string
	Error       string
	Summary     string
	NextRunAtMs int64
}

// CronServiceDeps provides integration hooks.
type CronServiceDeps struct {
	NowMs             func() int64
	Log               Logger
	StorePath         string
	Store             StoreBackend
	MaxConcurrentRuns int
	CronEnabled       bool

	// Optional hard timeout override (unix ms). If nil, cron derives a default from job config.
	ResolveJobTimeoutMs func(job CronJob) int64

	EnqueueSystemEvent  func(ctx context.Context, text string, agentID string) error
	RequestHeartbeatNow func(ctx context.Context, reason string)
	RunHeartbeatOnce    func(ctx context.Context, reason string) HeartbeatRunResult
	RunIsolatedAgentJob func(ctx context.Context, job CronJob, message string) (status string, summary string, outputText string, err error)

	OnEvent func(evt CronEvent)
}

type cronTask struct {
	jobID  string
	forced bool
	resp   chan cronTaskResult
}

type cronTaskResult struct {
	ran    bool
	reason string
	err    error
}

// CronService schedules jobs and runs them with a worker pool.
// The scheduler never executes jobs inline; it only enqueues tasks.
type CronService struct {
	deps           CronServiceDeps
	store          *CronStoreFile
	warnedDisabled bool

	mu      sync.Mutex
	started bool
	ctx     context.Context
	cancel  context.CancelFunc

	wakeCh chan struct{}
	taskCh chan cronTask

	qmu      sync.Mutex
	queued   map[string]struct{}
	inFlight map[string]struct{}

	schedulerWg sync.WaitGroup
	workersWg   sync.WaitGroup
}

func (c *CronService) withStoreLock(fn func() error) error {
	lock := storeLockForPath(c.deps.StorePath)
	lock.Lock()
	defer lock.Unlock()
	return fn()
}

// NewCronService creates a new cron service.
func NewCronService(deps CronServiceDeps) *CronService {
	if deps.NowMs == nil {
		deps.NowMs = func() int64 { return time.Now().UnixMilli() }
	}
	return &CronService{deps: deps}
}

// Start initializes the scheduler. It is safe to call Start multiple times.
func (c *CronService) Start() error {
	return c.withStoreLock(func() error {
		c.mu.Lock()
		if c.started {
			c.mu.Unlock()
			return nil
		}
		if !c.deps.CronEnabled {
			c.logInfo("cron: disabled", map[string]any{"enabled": false})
			c.mu.Unlock()
			return nil
		}
		if err := c.ensureLoadedLocked(true); err != nil {
			c.mu.Unlock()
			return err
		}
		// Normalize store so overdue jobs remain due (app may have been closed for days).
		if recomputeNextRuns(c.store, c.deps.NowMs(), c.deps.Log) {
			if err := c.persistLocked(); err != nil {
				c.mu.Unlock()
				return err
			}
		}

		c.ctx, c.cancel = context.WithCancel(context.Background())
		c.wakeCh = make(chan struct{}, 1)
		c.taskCh = make(chan cronTask, 1024)
		c.queued = make(map[string]struct{})
		c.inFlight = make(map[string]struct{})

		workers := c.deps.MaxConcurrentRuns
		if workers < 1 {
			workers = 1
		}
		for range workers {
			c.workersWg.Add(1)
			go c.workerLoop()
		}
		c.schedulerWg.Add(1)
		go c.schedulerLoop()

		c.started = true
		c.mu.Unlock()

		// Kick once on app-open so overdue jobs enqueue immediately.
		c.wakeScheduler()

		c.logInfo("cron: started", map[string]any{
			"enabled":      true,
			"jobs":         len(c.store.Jobs),
			"nextWakeAtMs": nextWakeAtMs(c.store),
		})
		return nil
	})
}

// Stop stops the scheduler and waits for workers to exit.
func (c *CronService) Stop() {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return
	}
	cancel := c.cancel
	c.started = false
	c.mu.Unlock()

	c.logInfo("cron: stopping scheduler", nil)

	if cancel != nil {
		cancel()
	}
	c.schedulerWg.Wait()
	c.workersWg.Wait()
}

// Status returns scheduler status.
func (c *CronService) Status() (bool, string, int, *int64, error) {
	var enabled bool
	var storePath string
	var jobs int
	var next *int64
	err := c.withStoreLock(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if err := c.ensureLoadedLocked(false); err != nil {
			return err
		}
		enabled = c.deps.CronEnabled
		storePath = c.deps.StorePath
		jobs = len(c.store.Jobs)
		if c.deps.CronEnabled {
			next = nextWakeAtMs(c.store)
		}
		return nil
	})
	if err != nil {
		return false, c.deps.StorePath, 0, nil, err
	}
	return enabled, storePath, jobs, next, nil
}

// List returns jobs.
func (c *CronService) List(includeDisabled bool) ([]CronJob, error) {
	var jobs []CronJob
	err := c.withStoreLock(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if err := c.ensureLoadedLocked(false); err != nil {
			return err
		}
		var list []CronJob
		for _, job := range c.store.Jobs {
			if includeDisabled || job.Enabled {
				list = append(list, job)
			}
		}
		sortJobs(list)
		jobs = list
		return nil
	})
	if err != nil {
		return nil, err
	}
	return jobs, nil
}

// Add creates a job.
func (c *CronService) Add(input CronJobCreate) (CronJob, error) {
	var job CronJob
	err := c.withStoreLock(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.warnIfDisabled("add")
		if err := c.ensureLoadedLocked(false); err != nil {
			return err
		}
		created, err := createJob(c.deps.NowMs(), input)
		if err != nil {
			return err
		}
		c.store.Jobs = append(c.store.Jobs, created)
		if err := c.persistLocked(); err != nil {
			return err
		}
		c.emit(CronEvent{JobID: created.ID, Action: "added", NextRunAtMs: ptr.Val(created.State.NextRunAtMs)})
		job = created
		return nil
	})
	if err != nil {
		return CronJob{}, err
	}
	c.wakeScheduler()
	return job, nil
}

// Update modifies a job.
func (c *CronService) Update(id string, patch CronJobPatch) (CronJob, error) {
	var job CronJob
	err := c.withStoreLock(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.warnIfDisabled("update")
		if err := c.ensureLoadedLocked(false); err != nil {
			return err
		}
		idx := findJobIndex(c.store.Jobs, id)
		if idx == -1 {
			return fmt.Errorf("unknown cron job id: %s", id)
		}
		current := c.store.Jobs[idx]
		if err := applyJobPatch(&current, patch); err != nil {
			return err
		}
		current.UpdatedAtMs = c.deps.NowMs()
		if current.Enabled {
			current.State.NextRunAtMs = computeJobNextRunAtMs(current, c.deps.NowMs())
		} else {
			current.State.NextRunAtMs = nil
			current.State.RunningAtMs = nil
		}
		c.store.Jobs[idx] = current
		if err := c.persistLocked(); err != nil {
			return err
		}
		c.emit(CronEvent{JobID: current.ID, Action: "updated", NextRunAtMs: ptr.Val(current.State.NextRunAtMs)})
		job = current
		return nil
	})
	if err != nil {
		return CronJob{}, err
	}
	c.wakeScheduler()
	return job, nil
}

// Remove deletes a job.
func (c *CronService) Remove(id string) (bool, error) {
	var removed bool
	err := c.withStoreLock(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.warnIfDisabled("remove")
		if err := c.ensureLoadedLocked(false); err != nil {
			return err
		}
		before := len(c.store.Jobs)
		c.store.Jobs = slices.DeleteFunc(c.store.Jobs, func(job CronJob) bool {
			return job.ID == id
		})
		removed = len(c.store.Jobs) != before
		if err := c.persistLocked(); err != nil {
			return err
		}
		if removed {
			c.emit(CronEvent{JobID: id, Action: "removed"})
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	c.wakeScheduler()
	return removed, nil
}

// Run executes a job if due (or forced). This call blocks until the run completes (or errors).
func (c *CronService) Run(id string, mode string) (bool, string, error) {
	c.warnIfDisabled("run")
	forced := mode == "force"

	resCh := make(chan cronTaskResult, 1)
	task := cronTask{jobID: id, forced: forced, resp: resCh}
	if err := c.enqueueTask(task, true); err != nil {
		return false, "", err
	}
	res := <-resCh
	if res.err != nil {
		return false, "", res.err
	}
	return res.ran, res.reason, nil
}

// Wake enqueues a system event.
func (c *CronService) Wake(mode string, text string) (bool, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false, nil
	}
	if c.deps.EnqueueSystemEvent == nil {
		return false, errors.New("enqueueSystemEvent not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.deps.EnqueueSystemEvent(ctx, trimmed, ""); err != nil {
		return false, err
	}
	c.logDebug("cron: wake event enqueued", map[string]any{"mode": mode, "text": trimmed})
	if mode == "now" && c.deps.RequestHeartbeatNow != nil {
		c.logDebug("cron: requesting immediate heartbeat for wake", nil)
		c.deps.RequestHeartbeatNow(ctx, "wake")
	}
	return true, nil
}

func (c *CronService) wakeScheduler() {
	c.mu.Lock()
	ch := c.wakeCh
	ctx := c.ctx
	started := c.started
	c.mu.Unlock()
	if !started || ch == nil || ctx == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (c *CronService) enqueueTask(task cronTask, allowForce bool) error {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return errors.New("cron scheduler not started")
	}
	ctx := c.ctx
	c.mu.Unlock()
	if ctx == nil {
		return errors.New("cron scheduler not started")
	}
	if task.forced && !allowForce {
		return errors.New("forced enqueue not allowed")
	}

	if task.resp == nil && !task.forced {
		c.qmu.Lock()
		if _, ok := c.queued[task.jobID]; ok {
			c.qmu.Unlock()
			return nil
		}
		if _, ok := c.inFlight[task.jobID]; ok {
			c.qmu.Unlock()
			return nil
		}
		// Reserve a spot in dedupe set before we try to send.
		c.queued[task.jobID] = struct{}{}
		c.qmu.Unlock()
	}

	select {
	case c.taskCh <- task:
		return nil
	case <-ctx.Done():
		return errors.New("cron scheduler stopped")
	default:
		// If we reserved a queued marker, roll it back.
		if task.resp == nil && !task.forced {
			c.qmu.Lock()
			delete(c.queued, task.jobID)
			c.qmu.Unlock()
		}
		return errors.New("cron task queue full")
	}
}

const (
	schedulerErrorBackoff = 30 * time.Second
	retrySoonDelay        = 1 * time.Second
	maxTimeoutMs          = int64((1 << 31) - 1)
)

func (c *CronService) schedulerLoop() {
	defer c.schedulerWg.Done()

	var timer *time.Timer
	var timerCh <-chan time.Time

	stopTimer := func() {
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		timerCh = nil
	}

	resetTimer := func(delay time.Duration) {
		if delay < 0 {
			stopTimer()
			return
		}
		if delay > time.Duration(maxTimeoutMs)*time.Millisecond {
			delay = time.Duration(maxTimeoutMs) * time.Millisecond
		}
		if timer == nil {
			timer = time.NewTimer(delay)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(delay)
		}
		timerCh = timer.C
	}

	for {
		select {
		case <-c.ctx.Done():
			stopTimer()
			return
		case <-c.wakeCh:
		case <-timerCh:
			c.logDebug("cron: timer tick fired", nil)
		}

		delay, err := c.scheduleOnce()
		if err != nil {
			c.logWarn("cron: scheduler tick failed, retrying", map[string]any{"error": err.Error()})
			resetTimer(schedulerErrorBackoff)
			continue
		}
		if delay >= 0 {
			c.logDebug("cron: timer armed", map[string]any{"delayMs": int64(delay / time.Millisecond)})
		}
		resetTimer(delay)
	}
}

func (c *CronService) scheduleOnce() (time.Duration, error) {
	now := c.deps.NowMs()

	var due []string
	var nextFuture *int64
	retrySoon := false

	err := c.withStoreLock(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()

		if err := c.ensureLoadedLocked(true); err != nil {
			return err
		}
		// Normalize + clear stuck markers. Keep overdue nextRunAtMs so the job runs once on app-open.
		mutated := recomputeNextRuns(c.store, now, c.deps.Log)
		if mutated {
			if err := c.persistLocked(); err != nil {
				return err
			}
		}

		for _, job := range c.store.Jobs {
			if !job.Enabled || job.State.RunningAtMs != nil || job.State.NextRunAtMs == nil {
				continue
			}
			if now >= *job.State.NextRunAtMs {
				due = append(due, job.ID)
			} else {
				val := *job.State.NextRunAtMs
				if nextFuture == nil || val < *nextFuture {
					nextFuture = &val
				}
			}
		}
		return nil
	})
	if err != nil {
		return -1, err
	}

	// Enqueue due jobs outside the store lock.
	if len(due) > 0 {
		c.logInfo("cron: timer tick processing", map[string]any{"due_jobs": len(due), "job_ids": due})
	}
	for _, id := range due {
		if err := c.enqueueTask(cronTask{jobID: id}, false); err != nil {
			// Queue full: retry soon. If the error is "not started" we are stopping.
			if strings.Contains(err.Error(), "queue full") {
				retrySoon = true
				break
			}
		}
	}

	// If we have due jobs queued/in-flight, rely on worker-finished wake to re-tick.
	if nextFuture == nil {
		if retrySoon {
			return retrySoonDelay, nil
		}
		return -1, nil
	}

	delayMs := max(0, *nextFuture-now)
	delay := time.Duration(delayMs) * time.Millisecond
	if retrySoon && delay > retrySoonDelay {
		delay = retrySoonDelay
	}
	return delay, nil
}

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
	// Dedupe accounting.
	if task.resp == nil && !task.forced {
		c.qmu.Lock()
		delete(c.queued, task.jobID)
		c.inFlight[task.jobID] = struct{}{}
		c.qmu.Unlock()
	} else if task.resp != nil {
		c.qmu.Lock()
		c.inFlight[task.jobID] = struct{}{}
		c.qmu.Unlock()
	}

	defer func() {
		c.qmu.Lock()
		delete(c.inFlight, task.jobID)
		c.qmu.Unlock()
		// Wake scheduler to recompute next wake time / enqueue more due jobs.
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
			return err
		}
		idx := findJobIndex(c.store.Jobs, jobID)
		if idx == -1 {
			return fmt.Errorf("unknown cron job id: %s", jobID)
		}
		job := c.store.Jobs[idx]
		if !job.Enabled {
			return errJobNotRunnable("disabled")
		}

		// If the job looks stuck, clear marker so we can run it.
		if job.State.RunningAtMs != nil && now-*job.State.RunningAtMs > stuckRunMs {
			job.State.RunningAtMs = nil
		}
		if job.State.RunningAtMs != nil {
			return errJobNotRunnable("already-running")
		}

		if !forced {
			if job.State.NextRunAtMs == nil || now < *job.State.NextRunAtMs {
				return errJobNotRunnable("not-due")
			}
		}

		startedAt = now
		job.State.RunningAtMs = &startedAt
		job.State.LastError = ""
		c.store.Jobs[idx] = job
		c.emit(CronEvent{JobID: job.ID, Action: "started", RunAtMs: startedAt})
		c.logInfo("cron: job starting", map[string]any{
			"jobId":   job.ID,
			"name":    job.Name,
			"session": string(job.SessionTarget),
			"payload": job.Payload.Kind,
		})
		if err := c.persistLocked(); err != nil {
			c.logWarn("cron: failed to persist started marker", map[string]any{"jobId": job.ID, "error": err.Error()})
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
	timeout := c.resolveJobTimeout(snapshot)
	jobCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	statusVal, errVal, summaryVal, _ := c.runJob(jobCtx, snapshot)

	// Phase 3: finalize under store lock.
	var deleted bool
	finishErr := c.withStoreLock(func() error {
		c.mu.Lock()
		defer c.mu.Unlock()

		if err := c.ensureLoadedLocked(true); err != nil {
			return err
		}
		idx := findJobIndex(c.store.Jobs, jobID)
		if idx == -1 {
			return nil
		}
		job := c.store.Jobs[idx]
		endedAt := c.deps.NowMs()

		job.State.RunningAtMs = nil
		job.State.LastRunAtMs = &startedAt
		job.State.LastStatus = statusVal
		job.State.LastDurationMs = ptr.Ptr(max(0, endedAt-startedAt))
		job.State.LastError = errVal
		job.UpdatedAtMs = endedAt

		shouldDelete := job.Schedule.Kind == "at" && statusVal == "ok" && job.DeleteAfterRun
		if !shouldDelete {
			if job.Schedule.Kind == "at" && statusVal == "ok" {
				job.Enabled = false
				job.State.NextRunAtMs = nil
			} else if job.Enabled {
				job.State.NextRunAtMs = computeJobNextRunAtMs(job, endedAt)
			} else {
				job.State.NextRunAtMs = nil
			}
		}

		c.emit(CronEvent{
			JobID:       job.ID,
			Action:      "finished",
			RunAtMs:     startedAt,
			DurationMs:  max(0, endedAt-startedAt),
			Status:      statusVal,
			Error:       errVal,
			Summary:     summaryVal,
			NextRunAtMs: ptr.Val(job.State.NextRunAtMs),
		})
		c.logInfo("cron: job finished", map[string]any{
			"jobId":      job.ID,
			"name":       job.Name,
			"status":     statusVal,
			"error":      errVal,
			"durationMs": max(0, endedAt-startedAt),
		})

		if shouldDelete {
			c.store.Jobs = slices.DeleteFunc(c.store.Jobs, func(existing CronJob) bool {
				return existing.ID == job.ID
			})
			c.emit(CronEvent{JobID: job.ID, Action: "removed"})
			deleted = true
		} else {
			c.store.Jobs[idx] = job
		}
		if err := c.persistLocked(); err != nil {
			c.logWarn("cron: failed to persist after job finished", map[string]any{"jobId": job.ID, "error": err.Error()})
		}
		return nil
	})
	if finishErr != nil {
		// Prefer surfacing execution error, but finish errors are also important.
		if err == nil {
			err = finishErr
		} else {
			c.logWarn("cron: finalize failed after run", map[string]any{"jobId": snapshot.ID, "error": finishErr.Error()})
		}
	}

	// Post summary back to main session for isolated jobs (best-effort).
	if !deleted && snapshot.SessionTarget == CronSessionIsolated {
		summaryText := strings.TrimSpace(summaryVal)
		deliveryMode := CronDeliveryAnnounce
		if snapshot.Delivery != nil && strings.TrimSpace(string(snapshot.Delivery.Mode)) != "" {
			deliveryMode = snapshot.Delivery.Mode
		}
		if summaryText != "" && deliveryMode != CronDeliveryNone && c.deps.EnqueueSystemEvent != nil {
			label := "Cron: " + summaryText
			if statusVal != "ok" {
				label = fmt.Sprintf("Cron (%s): %s", statusVal, summaryText)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = c.deps.EnqueueSystemEvent(ctx, strings.TrimSpace(label), snapshot.AgentID)
			cancel()
			if snapshot.WakeMode == CronWakeNow && c.deps.RequestHeartbeatNow != nil {
				ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
				c.deps.RequestHeartbeatNow(ctx2, "cron:"+snapshot.ID+":post")
				cancel2()
			}
		}
	}

	if err != nil {
		return true, "", err
	}
	return true, "", nil
}

func (c *CronService) runJob(ctx context.Context, job CronJob) (statusVal, errVal, summaryVal, outputVal string) {
	if job.SessionTarget == CronSessionMain {
		text, reason := resolveJobPayloadTextForMain(job)
		if strings.TrimSpace(text) == "" {
			return "skipped", reason, "", ""
		}
		if c.deps.EnqueueSystemEvent == nil {
			return "error", "enqueueSystemEvent not configured", "", ""
		}
		if err := c.deps.EnqueueSystemEvent(ctx, text, job.AgentID); err != nil {
			// Enqueue failure shouldn't wedge cron; record error.
			return "error", err.Error(), text, ""
		}

		if job.WakeMode == CronWakeNow && c.deps.RunHeartbeatOnce != nil {
			reason := "cron:" + job.ID
			maxWait := 2 * time.Minute
			waitStarted := time.Now()
			for {
				if ctx.Err() != nil {
					return "error", "cron job timed out", text, ""
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

	if strings.ToLower(job.Payload.Kind) != "agentturn" {
		return "skipped", "isolated job requires payload.kind=agentTurn", "", ""
	}
	if c.deps.RunIsolatedAgentJob == nil {
		return "error", "isolated cron jobs not supported", "", ""
	}
	status, summary, output, runErr := c.deps.RunIsolatedAgentJob(ctx, job, job.Payload.Message)
	if runErr != nil {
		return "error", runErr.Error(), summary, output
	}
	switch status {
	case "ok":
		return "ok", "", summary, output
	case "skipped":
		return "skipped", "", summary, output
	default:
		return "error", "cron job failed", summary, output
	}
}

func (c *CronService) resolveJobTimeout(job CronJob) time.Duration {
	if c.deps.ResolveJobTimeoutMs != nil {
		if ms := c.deps.ResolveJobTimeoutMs(job); ms > 0 {
			return clampDuration(time.Duration(ms) * time.Millisecond)
		}
	}

	// Defaults: 10 minutes, with isolated override via payload.timeoutSeconds.
	timeout := 10 * time.Minute
	if job.SessionTarget == CronSessionIsolated {
		timeout = 10 * time.Minute
		if job.Payload.TimeoutSeconds != nil {
			seconds := *job.Payload.TimeoutSeconds
			switch {
			case seconds == 0:
				timeout = 30 * 24 * time.Hour
			case seconds > 0:
				timeout = time.Duration(seconds) * time.Second
			}
		}
	}
	return clampDuration(timeout)
}

func clampDuration(d time.Duration) time.Duration {
	if d < 1*time.Second {
		return 1 * time.Second
	}
	max := 30 * 24 * time.Hour
	if d > max {
		return max
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

func (c *CronService) ensureLoadedLocked(forceReload bool) error {
	if forceReload {
		c.store = nil
		clearCachedStore(c.deps.StorePath)
	}
	if c.store != nil {
		return nil
	}
	if !forceReload {
		if cached := getCachedStore(c.deps.StorePath); cached != nil {
			c.store = cached
			return nil
		}
	}
	if c.deps.Store == nil {
		return errors.New("cron store backend not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := LoadCronStore(ctx, c.deps.Store, c.deps.StorePath)
	if err != nil {
		return err
	}
	c.store = &store

	// fix names/description
	mutated := false
	for i := range c.store.Jobs {
		job := c.store.Jobs[i]
		name := strings.TrimSpace(job.Name)
		if name == "" {
			name = inferLegacyName(&CronJobCreate{Payload: job.Payload, Schedule: job.Schedule})
			mutated = true
		} else if name != job.Name {
			mutated = true
		}
		job.Name = name
		if strings.TrimSpace(job.Description) != job.Description {
			job.Description = strings.TrimSpace(job.Description)
			mutated = true
		}
		c.store.Jobs[i] = job
	}
	setCachedStore(c.deps.StorePath, c.store)
	if mutated {
		return c.persistLocked()
	}
	return nil
}

func (c *CronService) persistLocked() error {
	if c.store == nil {
		return nil
	}
	if c.deps.Store == nil {
		return errors.New("cron store backend not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := SaveCronStore(ctx, c.deps.Store, c.deps.StorePath, *c.store)
	if err == nil {
		clearCachedStore(c.deps.StorePath)
	}
	return err
}

func (c *CronService) warnIfDisabled(action string) {
	if c.deps.CronEnabled {
		return
	}
	if c.warnedDisabled {
		return
	}
	c.warnedDisabled = true
	c.logWarn("cron: scheduler disabled; jobs will not run automatically", map[string]any{
		"enabled":   false,
		"action":    action,
		"storePath": c.deps.StorePath,
	})
}

func (c *CronService) emit(evt CronEvent) {
	if c.deps.OnEvent == nil {
		return
	}
	c.deps.OnEvent(evt)
}

func (c *CronService) logInfo(msg string, fields map[string]any) {
	if c.deps.Log != nil {
		c.deps.Log.Info(msg, fields)
	}
}

func (c *CronService) logDebug(msg string, fields map[string]any) {
	if c.deps.Log != nil {
		c.deps.Log.Debug(msg, fields)
	}
}

func (c *CronService) logWarn(msg string, fields map[string]any) {
	if c.deps.Log != nil {
		c.deps.Log.Warn(msg, fields)
	}
}
