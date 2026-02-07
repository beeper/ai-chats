package cron

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// failingStoreBackend returns errors on Read/Write when configured to do so.
type failingStoreBackend struct {
	inner     StoreBackend
	failReads atomic.Bool
}

func (b *failingStoreBackend) Read(ctx context.Context, path string) ([]byte, bool, error) {
	if b.failReads.Load() {
		return nil, false, errors.New("injected read error")
	}
	return b.inner.Read(ctx, path)
}

func (b *failingStoreBackend) Write(ctx context.Context, path string, data []byte) error {
	return b.inner.Write(ctx, path, data)
}

// testLogger captures log messages for assertions.
type testLogger struct {
	mu       sync.Mutex
	warnings []string
	infos    []string
}

func (l *testLogger) Debug(_ string, _ ...any) {}
func (l *testLogger) Info(msg string, _ ...any) {
	l.mu.Lock()
	l.infos = append(l.infos, msg)
	l.mu.Unlock()
}
func (l *testLogger) Warn(msg string, _ ...any) {
	l.mu.Lock()
	l.warnings = append(l.warnings, msg)
	l.mu.Unlock()
}
func (l *testLogger) Error(_ string, _ ...any) {}

func (l *testLogger) hasWarning(substr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, w := range l.warnings {
		if len(w) >= len(substr) && contains(w, substr) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func newTestService(backend StoreBackend, log Logger) *CronService {
	now := int64(1700000000000)
	return NewCronService(CronServiceDeps{
		NowMs:       func() int64 { return now },
		Log:         log,
		StorePath:   "cron/jobs.json",
		Store:       backend,
		CronEnabled: true,
	})
}

func validStoreJSON() []byte {
	return []byte(`{
  "version": 1,
  "jobs": [
    {
      "id": "job-1",
      "name": "Test job",
      "enabled": true,
      "createdAtMs": 1700000000000,
      "updatedAtMs": 1700000000000,
      "schedule": { "kind": "every", "everyMs": 60000 },
      "sessionTarget": "main",
      "wakeMode": "next-heartbeat",
      "payload": { "kind": "systemEvent", "text": "ping" },
      "state": { "nextRunAtMs": 1699999990000 }
    }
  ]
}`)
}

// Bug #1: Timer re-arms on ensureLoaded failure
func TestOnTimerRearmsOnEnsureLoadedFailure(t *testing.T) {
	log := &testLogger{}
	backend := &failingStoreBackend{inner: &testStoreBackend{}}
	svc := newTestService(backend, log)
	svc.deps.CronEnabled = true

	// Make reads fail so ensureLoaded returns an error.
	backend.failReads.Store(true)

	// Call onTimer directly — it should not panic and should re-arm.
	svc.onTimer()

	// The timer should have been re-armed with a backoff delay.
	svc.mu.Lock()
	timerSet := svc.timer != nil
	svc.mu.Unlock()
	if !timerSet {
		t.Fatal("expected timer to be re-armed after ensureLoaded failure")
	}

	if !log.hasWarning("ensureLoaded failed") {
		t.Fatal("expected warning about ensureLoaded failure")
	}

	svc.Stop()
}

// Bug #4: Corrupt store falls back to .bak
func TestLoadCronStoreCorruptFallsBackToBak(t *testing.T) {
	const storePath = "cron/jobs.json"
	backend := &testStoreBackend{
		files: map[string][]byte{
			storePath:          []byte(`{corrupt json!!!`),
			storePath + ".bak": validStoreJSON(),
		},
	}

	store, err := LoadCronStore(context.Background(), backend, storePath)
	if err != nil {
		t.Fatalf("expected .bak fallback to succeed, got error: %v", err)
	}
	if len(store.Jobs) != 1 {
		t.Fatalf("expected 1 job from .bak, got %d", len(store.Jobs))
	}
	if store.Jobs[0].ID != "job-1" {
		t.Fatalf("expected job id job-1, got %q", store.Jobs[0].ID)
	}
}

func TestLoadCronStoreBothCorruptReturnsError(t *testing.T) {
	const storePath = "cron/jobs.json"
	backend := &testStoreBackend{
		files: map[string][]byte{
			storePath:          []byte(`{corrupt`),
			storePath + ".bak": []byte(`also corrupt`),
		},
	}

	_, err := LoadCronStore(context.Background(), backend, storePath)
	if err == nil {
		t.Fatal("expected error when both main and .bak are corrupt")
	}
}

func TestLoadCronStoreCorruptNoBakReturnsError(t *testing.T) {
	const storePath = "cron/jobs.json"
	backend := &testStoreBackend{
		files: map[string][]byte{
			storePath: []byte(`{corrupt`),
		},
	}

	_, err := LoadCronStore(context.Background(), backend, storePath)
	if err == nil {
		t.Fatal("expected error when main is corrupt and no .bak exists")
	}
}

// Gap #6: onTimer force-reloads from DB
func TestOnTimerForceReloadsFromDB(t *testing.T) {
	log := &testLogger{}
	backend := &testStoreBackend{
		files: map[string][]byte{
			"cron/jobs.json": validStoreJSON(),
		},
	}
	svc := newTestService(backend, log)
	svc.deps.EnqueueSystemEvent = func(text string, agentID string) error { return nil }

	// Pre-load the store.
	if err := svc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Externally modify the store to add a second job.
	backend.files["cron/jobs.json"] = []byte(`{
  "version": 1,
  "jobs": [
    {
      "id": "job-1",
      "name": "Test job",
      "enabled": true,
      "createdAtMs": 1700000000000,
      "updatedAtMs": 1700000000000,
      "schedule": { "kind": "every", "everyMs": 60000 },
      "sessionTarget": "main",
      "wakeMode": "next-heartbeat",
      "payload": { "kind": "systemEvent", "text": "ping" },
      "state": { "nextRunAtMs": 1699999990000 }
    },
    {
      "id": "job-2",
      "name": "New job",
      "enabled": true,
      "createdAtMs": 1700000000000,
      "updatedAtMs": 1700000000000,
      "schedule": { "kind": "every", "everyMs": 60000 },
      "sessionTarget": "main",
      "wakeMode": "next-heartbeat",
      "payload": { "kind": "systemEvent", "text": "pong" },
      "state": { "nextRunAtMs": 1700000060000 }
    }
  ]
}`)
	// Clear the cache to simulate external change being visible.
	clearCachedStore("cron/jobs.json")

	// Trigger onTimer — it should force-reload and see both jobs.
	svc.onTimer()

	jobs, err := svc.List(true)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs after reload, got %d", len(jobs))
	}

	svc.Stop()
}

// Robustness #11: sortJobs puts nil NextRunAtMs at end
func TestSortJobsNilNextRunAtEnd(t *testing.T) {
	past := int64(1000)
	future := int64(9999)
	jobs := []CronJob{
		{ID: "disabled", State: CronJobState{NextRunAtMs: nil}},
		{ID: "future", State: CronJobState{NextRunAtMs: &future}},
		{ID: "past", State: CronJobState{NextRunAtMs: &past}},
		{ID: "also-disabled", State: CronJobState{NextRunAtMs: nil}},
	}
	sortJobs(jobs)

	if jobs[0].ID != "past" {
		t.Fatalf("expected first job to be 'past', got %q", jobs[0].ID)
	}
	if jobs[1].ID != "future" {
		t.Fatalf("expected second job to be 'future', got %q", jobs[1].ID)
	}
	// Last two should be the nil ones (disabled).
	if jobs[2].State.NextRunAtMs != nil || jobs[3].State.NextRunAtMs != nil {
		t.Fatal("expected nil NextRunAtMs jobs at end")
	}
}

// Bug #2: computeJobNextRunAtMs called once in finish (no double computation)
// We verify indirectly: a recurring job's NextRunAtMs should be based on endedAt.
func TestFinishComputesNextRunOnce(t *testing.T) {
	log := &testLogger{}
	nowMs := int64(1700000060000)
	backend := &testStoreBackend{
		files: map[string][]byte{
			"cron/jobs.json": []byte(`{
  "version": 1,
  "jobs": [
    {
      "id": "recurring-1",
      "name": "Recurring",
      "enabled": true,
      "createdAtMs": 1700000000000,
      "updatedAtMs": 1700000000000,
      "schedule": { "kind": "every", "everyMs": 60000 },
      "sessionTarget": "main",
      "wakeMode": "next-heartbeat",
      "payload": { "kind": "systemEvent", "text": "tick" },
      "state": { "nextRunAtMs": 1700000000000 }
    }
  ]
}`),
		},
	}

	enqueued := false
	svc := NewCronService(CronServiceDeps{
		NowMs:       func() int64 { return nowMs },
		Log:         log,
		StorePath:   "cron/jobs.json",
		Store:       backend,
		CronEnabled: true,
		EnqueueSystemEvent: func(text string, agentID string) error {
			enqueued = true
			return nil
		},
		RequestHeartbeatNow: func(reason string) {},
	})

	if err := svc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Run the job.
	ran, reason, err := svc.Run("recurring-1", "force")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !ran {
		t.Fatalf("expected job to run, reason: %s", reason)
	}
	if !enqueued {
		t.Fatal("expected system event to be enqueued")
	}

	// Check the job state — NextRunAtMs should be set and > nowMs.
	jobs, err := svc.List(true)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].State.NextRunAtMs == nil {
		t.Fatal("expected NextRunAtMs to be set after finish")
	}
	if *jobs[0].State.NextRunAtMs <= nowMs {
		t.Fatalf("expected NextRunAtMs > %d, got %d", nowMs, *jobs[0].State.NextRunAtMs)
	}

	svc.Stop()
}

// Robustness #9: Stop waits for in-flight execution
func TestStopWaitsForInflightExecution(t *testing.T) {
	log := &testLogger{}
	// Use an advancing clock: Start sees t=0, onTimer sees t=60001 (job is due).
	var clock atomic.Int64
	clock.Store(1700000000000)
	backend := &testStoreBackend{
		files: map[string][]byte{
			"cron/jobs.json": []byte(`{
  "version": 1,
  "jobs": [
    {
      "id": "slow-job",
      "name": "Slow",
      "enabled": true,
      "createdAtMs": 1700000000000,
      "updatedAtMs": 1700000000000,
      "schedule": { "kind": "every", "everyMs": 60000 },
      "sessionTarget": "isolated",
      "wakeMode": "next-heartbeat",
      "payload": { "kind": "agentTurn", "message": "slow task" },
      "state": { "nextRunAtMs": 1700000060000 }
    }
  ]
}`),
		},
	}

	var jobStarted atomic.Bool
	svc := NewCronService(CronServiceDeps{
		NowMs:       func() int64 { return clock.Load() },
		Log:         log,
		StorePath:   "cron/jobs.json",
		Store:       backend,
		CronEnabled: true,
		RunIsolatedAgentJob: func(job CronJob, message string) (string, string, string, error) {
			jobStarted.Store(true)
			time.Sleep(200 * time.Millisecond)
			return "ok", "done", "output", nil
		},
	})

	if err := svc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Advance clock past the due time.
	clock.Store(1700000060001)

	// Trigger execution in background.
	go svc.onTimer()

	// Wait for job to start executing.
	deadline := time.Now().Add(2 * time.Second)
	for !jobStarted.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !jobStarted.Load() {
		t.Fatal("job never started")
	}

	// Stop should block until the job finishes.
	stopDone := make(chan struct{})
	go func() {
		svc.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// Stop returned — verify running is false.
		svc.mu.Lock()
		running := svc.running
		svc.mu.Unlock()
		if running {
			t.Fatal("expected running to be false after Stop")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within timeout")
	}
}

// Gap #5: Cache is invalidated on persist
func TestPersistInvalidatesCache(t *testing.T) {
	backend := &testStoreBackend{
		files: map[string][]byte{
			"cron/jobs.json": validStoreJSON(),
		},
	}
	log := &testLogger{}
	svc := newTestService(backend, log)

	if err := svc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Manually seed the cache to verify it gets cleared on persist.
	setCachedStore("cron/jobs.json", svc.store)
	cached := getCachedStore("cron/jobs.json")
	if cached == nil {
		t.Fatal("expected cache to be set after manual seed")
	}

	// Adding a job should persist and invalidate cache.
	_, err := svc.Add(CronJobCreate{
		Name:          "new job",
		Schedule:      CronSchedule{Kind: "every", EveryMs: 60000},
		SessionTarget: CronSessionMain,
		Payload:       CronPayload{Kind: "systemEvent", Text: "test"},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// After persist, cache should be cleared.
	cached = getCachedStore("cron/jobs.json")
	if cached != nil {
		t.Fatal("expected cache to be cleared after persist")
	}

	svc.Stop()
}
