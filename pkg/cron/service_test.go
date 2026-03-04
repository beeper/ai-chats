package cron

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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

func TestSchedulerRunsOverdueJobsOnStart(t *testing.T) {
	log := &testLogger{}
	now := time.Now().UnixMilli()
	backend := &testStoreBackend{
		files: map[string][]byte{
			"cron/jobs.json": []byte(`{
  "version": 1,
  "jobs": [
    {
      "id": "job-1",
      "name": "Overdue",
      "enabled": true,
      "createdAtMs": 1700000000000,
      "updatedAtMs": 1700000000000,
      "schedule": { "kind": "every", "everyMs": 1000 },
      "sessionTarget": "isolated",
      "wakeMode": "next-heartbeat",
      "payload": { "kind": "agentTurn", "message": "hi", "timeoutSeconds": 1 },
      "state": { "nextRunAtMs": 1 }
    }
  ]
}`),
		},
	}

	events := make(chan CronEvent, 10)
	svc := NewCronService(CronServiceDeps{
		NowMs:             func() int64 { return time.Now().UnixMilli() },
		Log:               log,
		StorePath:         "cron/jobs.json",
		Store:             backend,
		CronEnabled:       true,
		MaxConcurrentRuns: 1,
		ResolveJobTimeoutMs: func(job CronJob) int64 {
			_ = job
			return 500 // ms
		},
		RunIsolatedAgentJob: func(ctx context.Context, job CronJob, message string) (string, string, string, error) {
			_ = message
			if ctx.Err() != nil {
				return "error", "", "", ctx.Err()
			}
			return "ok", "done", "out", nil
		},
		OnEvent: func(evt CronEvent) { events <- evt },
	})

	if err := svc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer svc.Stop()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt := <-events:
			if evt.Action == "finished" && evt.JobID == "job-1" {
				if evt.Status != "ok" {
					t.Fatalf("expected ok, got %q (err=%q)", evt.Status, evt.Error)
				}
				// Sanity: finished after start time.
				if time.Now().UnixMilli() < now-10_000 {
					t.Fatal("unexpected clock skew in test")
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for overdue job to run on start")
		}
	}
}

func TestMaxConcurrentRunsEnforced(t *testing.T) {
	log := &testLogger{}
	backend := &testStoreBackend{
		files: map[string][]byte{
			"cron/jobs.json": []byte(`{
  "version": 1,
  "jobs": [
    {
      "id": "job-1",
      "name": "A",
      "enabled": true,
      "createdAtMs": 1700000000000,
      "updatedAtMs": 1700000000000,
      "schedule": { "kind": "every", "everyMs": 1000 },
      "sessionTarget": "isolated",
      "wakeMode": "next-heartbeat",
      "payload": { "kind": "agentTurn", "message": "a" },
      "state": { "nextRunAtMs": 1 }
    },
    {
      "id": "job-2",
      "name": "B",
      "enabled": true,
      "createdAtMs": 1700000000000,
      "updatedAtMs": 1700000000000,
      "schedule": { "kind": "every", "everyMs": 1000 },
      "sessionTarget": "isolated",
      "wakeMode": "next-heartbeat",
      "payload": { "kind": "agentTurn", "message": "b" },
      "state": { "nextRunAtMs": 1 }
    }
  ]
}`),
		},
	}

	var inFlight atomic.Int64
	var maxSeen atomic.Int64
	events := make(chan CronEvent, 20)

	svc := NewCronService(CronServiceDeps{
		NowMs:             func() int64 { return time.Now().UnixMilli() },
		Log:               log,
		StorePath:         "cron/jobs.json",
		Store:             backend,
		CronEnabled:       true,
		MaxConcurrentRuns: 1,
		ResolveJobTimeoutMs: func(job CronJob) int64 {
			_ = job
			return 2000
		},
		RunIsolatedAgentJob: func(ctx context.Context, job CronJob, message string) (string, string, string, error) {
			_ = job
			_ = message
			cur := inFlight.Add(1)
			for {
				prev := maxSeen.Load()
				if cur <= prev || maxSeen.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(150 * time.Millisecond)
			inFlight.Add(-1)
			return "ok", "done", "out", nil
		},
		OnEvent: func(evt CronEvent) { events <- evt },
	})

	if err := svc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer svc.Stop()

	finished := 0
	deadline := time.After(5 * time.Second)
	for finished < 2 {
		select {
		case evt := <-events:
			if evt.Action == "finished" {
				finished++
			}
		case <-deadline:
			t.Fatalf("timed out waiting for jobs to finish (finished=%d)", finished)
		}
	}

	if got := maxSeen.Load(); got != 1 {
		t.Fatalf("expected max in-flight=1, got %d", got)
	}
}

func TestRunForceQueuesAndWaits(t *testing.T) {
	log := &testLogger{}
	backend := &testStoreBackend{
		files: map[string][]byte{
			"cron/jobs.json": []byte(`{
  "version": 1,
  "jobs": [
    {
      "id": "job-1",
      "name": "Manual",
      "enabled": true,
      "createdAtMs": 1700000000000,
      "updatedAtMs": 1700000000000,
      "schedule": { "kind": "every", "everyMs": 60000 },
      "sessionTarget": "isolated",
      "wakeMode": "next-heartbeat",
      "payload": { "kind": "agentTurn", "message": "x" },
      "state": { "nextRunAtMs": 9999999999999 }
    }
  ]
}`),
		},
	}

	svc := NewCronService(CronServiceDeps{
		NowMs:             func() int64 { return time.Now().UnixMilli() },
		Log:               log,
		StorePath:         "cron/jobs.json",
		Store:             backend,
		CronEnabled:       true,
		MaxConcurrentRuns: 1,
		ResolveJobTimeoutMs: func(job CronJob) int64 {
			_ = job
			return 500
		},
		RunIsolatedAgentJob: func(ctx context.Context, job CronJob, message string) (string, string, string, error) {
			_ = ctx
			_ = job
			_ = message
			return "ok", "done", "out", nil
		},
	})
	if err := svc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer svc.Stop()

	ran, reason, err := svc.Run("job-1", "force")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !ran {
		t.Fatalf("expected ran=true, reason=%q", reason)
	}
}

func TestPersistInvalidatesCache(t *testing.T) {
	backend := &testStoreBackend{
		files: map[string][]byte{
			"cron/jobs.json": []byte(`{ "version": 1, "jobs": [] }`),
		},
	}
	log := &testLogger{}
	svc := NewCronService(CronServiceDeps{
		NowMs:       func() int64 { return 1700000000000 },
		Log:         log,
		StorePath:   "cron/jobs.json",
		Store:       backend,
		CronEnabled: true,
	})

	// Force a load so svc.store exists.
	if _, err := svc.List(true); err != nil {
		t.Fatalf("List failed: %v", err)
	}

	setCachedStore("cron/jobs.json", svc.store)
	if getCachedStore("cron/jobs.json") == nil {
		t.Fatal("expected cache to be set")
	}

	_, err := svc.Add(CronJobCreate{
		Name:          "new job",
		Schedule:      CronSchedule{Kind: "every", EveryMs: 60000},
		SessionTarget: CronSessionMain,
		Payload:       CronPayload{Kind: "systemEvent", Text: "test"},
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if getCachedStore("cron/jobs.json") != nil {
		t.Fatal("expected cache to be cleared after persist")
	}
}

func TestValidateScheduleRejectsInvalidTZ(t *testing.T) {
	result := ValidateSchedule(CronSchedule{Kind: "cron", Expr: "0 9 * * *", TZ: "American/New_York"})
	if result.Ok {
		t.Fatal("expected validation to fail for invalid timezone")
	}
	if !strings.Contains(result.Message, "not a valid IANA timezone") {
		t.Fatalf("unexpected message: %s", result.Message)
	}
}

func TestValidateScheduleAcceptsValidTZ(t *testing.T) {
	result := ValidateSchedule(CronSchedule{Kind: "cron", Expr: "0 9 * * *", TZ: "America/New_York"})
	if !result.Ok {
		t.Fatalf("expected validation to pass for valid timezone, got: %s", result.Message)
	}
}

func TestValidateScheduleAcceptsEmptyTZ(t *testing.T) {
	result := ValidateSchedule(CronSchedule{Kind: "cron", Expr: "0 9 * * *"})
	if !result.Ok {
		t.Fatalf("expected validation to pass for empty timezone, got: %s", result.Message)
	}
}

func TestValidateScheduleRejectsInvalidCronExpr(t *testing.T) {
	result := ValidateSchedule(CronSchedule{Kind: "cron", Expr: "not a cron expr"})
	if result.Ok {
		t.Fatal("expected validation to fail for invalid cron expression")
	}
	if !strings.Contains(result.Message, "Invalid schedule.expr") {
		t.Fatalf("unexpected message: %s", result.Message)
	}
}

func TestValidateScheduleRejectsEmptyCronExpr(t *testing.T) {
	result := ValidateSchedule(CronSchedule{Kind: "cron", Expr: ""})
	if result.Ok {
		t.Fatal("expected validation to fail for empty cron expression")
	}
}

func TestValidateScheduleSkipsNonCronKinds(t *testing.T) {
	// "every" schedule with a bogus TZ should still fail (TZ is validated regardless of kind).
	result := ValidateSchedule(CronSchedule{Kind: "every", EveryMs: 60000, TZ: "Fake/Zone"})
	if result.Ok {
		t.Fatal("expected validation to fail for invalid timezone even on non-cron schedule")
	}

	// "at" schedule with no TZ should pass.
	result = ValidateSchedule(CronSchedule{Kind: "at", At: "2025-06-01T00:00:00Z"})
	if !result.Ok {
		t.Fatalf("expected validation to pass for at schedule, got: %s", result.Message)
	}
}
