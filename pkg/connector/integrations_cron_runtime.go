package connector

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/cron"
)

type cronStoreBackendAdapter struct {
	backend *lazyStoreBackend
}

func (a *cronStoreBackendAdapter) Read(ctx context.Context, key string) ([]byte, bool, error) {
	if a == nil || a.backend == nil {
		return nil, false, errors.New("bridge state store not available")
	}
	return a.backend.Read(ctx, key)
}

func (a *cronStoreBackendAdapter) Write(ctx context.Context, key string, data []byte) error {
	if a == nil || a.backend == nil {
		return errors.New("bridge state store not available")
	}
	return a.backend.Write(ctx, key, data)
}

func (a *cronStoreBackendAdapter) List(ctx context.Context, prefix string) ([]cron.StoreEntry, error) {
	if a == nil || a.backend == nil {
		return nil, errors.New("bridge state store not available")
	}
	entries, err := a.backend.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]cron.StoreEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, cron.StoreEntry{Key: entry.Key, Data: entry.Data})
	}
	return out, nil
}

func resolveCronEnabled(cfg *Config) bool {
	if cfg == nil || cfg.Cron == nil || cfg.Cron.Enabled == nil {
		return true
	}
	return *cfg.Cron.Enabled
}

func resolveCronStorePath(cfg *Config) string {
	raw := ""
	if cfg != nil && cfg.Cron != nil {
		raw = cfg.Cron.Store
	}
	return cron.ResolveCronStorePath(raw)
}

func resolveCronMaxConcurrentRuns(cfg *Config) int {
	if cfg == nil || cfg.Cron == nil {
		return 1
	}
	if cfg.Cron.MaxConcurrentRuns > 0 {
		return cfg.Cron.MaxConcurrentRuns
	}
	return 1
}

func (oc *AIClient) buildCronService() *cron.CronService {
	if oc == nil {
		return nil
	}
	storePath := resolveCronStorePath(&oc.connector.Config)
	// Use a lazy wrapper so that each store operation gets a fresh backend
	// with the current loginID (survives reconnection without stale state).
	storeBackend := &cronStoreBackendAdapter{backend: &lazyStoreBackend{client: oc}}
	deps := cron.CronServiceDeps{
		NowMs:               func() int64 { return time.Now().UnixMilli() },
		Log:                 cronLogger{log: oc.log},
		StorePath:           storePath,
		Store:               storeBackend,
		MaxConcurrentRuns:   resolveCronMaxConcurrentRuns(&oc.connector.Config),
		CronEnabled:         resolveCronEnabled(&oc.connector.Config),
		ResolveJobTimeoutMs: func(job cron.CronJob) int64 { return oc.resolveCronJobTimeoutMs(job) },
		EnqueueSystemEvent: func(ctx context.Context, text string, agentID string) error {
			return oc.enqueueCronSystemEvent(ctx, text, agentID)
		},
		RequestHeartbeatNow: func(ctx context.Context, reason string) {
			oc.requestHeartbeatNow(ctx, reason)
		},
		RunHeartbeatOnce: func(ctx context.Context, reason string) cron.HeartbeatRunResult {
			res := oc.runHeartbeatImmediate(ctx, reason)
			return cron.HeartbeatRunResult{Status: res.Status, Reason: res.Reason}
		},
		RunIsolatedAgentJob: func(ctx context.Context, job cron.CronJob, message string) (string, string, string, error) {
			return oc.runCronIsolatedAgentJob(ctx, job, message)
		},
		OnEvent: oc.onCronEvent,
	}
	return cron.NewCronService(deps)
}

func (oc *AIClient) resolveCronJobTimeoutMs(job cron.CronJob) int64 {
	// Default to agent defaults for isolated jobs; main jobs use a short fixed default.
	if oc == nil {
		return 0
	}
	if job.SessionTarget != cron.CronSessionIsolated {
		return int64((10 * time.Minute) / time.Millisecond)
	}

	// Base default from config agents.defaults.timeoutSeconds (fallback 600s).
	defaultSeconds := 600
	if cfg := &oc.connector.Config; cfg != nil && cfg.Agents != nil && cfg.Agents.Defaults != nil && cfg.Agents.Defaults.TimeoutSeconds > 0 {
		defaultSeconds = cfg.Agents.Defaults.TimeoutSeconds
	}
	timeoutSeconds := defaultSeconds
	if job.Payload.TimeoutSeconds != nil {
		override := *job.Payload.TimeoutSeconds
		switch {
		case override == 0:
			return int64((30 * 24 * time.Hour) / time.Millisecond)
		case override > 0:
			timeoutSeconds = override
		}
	}
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}
	return int64(timeoutSeconds) * 1000
}

func (oc *AIClient) enqueueCronSystemEvent(ctx context.Context, text string, agentID string) error {
	if oc == nil {
		return errors.New("missing client")
	}
	agentID = resolveCronAgentID(agentID, &oc.connector.Config)
	hb := resolveHeartbeatConfig(&oc.connector.Config, agentID)
	portal, sessionKey, err := oc.resolveHeartbeatSessionPortal(agentID, hb)
	if err != nil || portal == nil || sessionKey == "" {
		if err != nil {
			oc.loggerForContext(context.Background()).Warn().Err(err).Str("agent_id", agentID).Msg("cron: unable to resolve heartbeat session for system event")
		}
		// Fallback to logical session key so the event isn't lost if room resolution is temporarily unavailable.
		sessionKey = strings.TrimSpace(oc.resolveHeartbeatSession(agentID, hb).SessionKey)
		if sessionKey == "" {
			return nil
		}
	}
	enqueueSystemEvent(sessionKey, text, agentID)
	persistSystemEventsSnapshot(oc.bridgeStateBackend(), oc.Log())
	oc.log.Debug().Str("session_key", sessionKey).Str("agent_id", agentID).Str("text", text).Msg("Cron system event enqueued")
	return nil
}

func (oc *AIClient) requestHeartbeatNow(ctx context.Context, reason string) {
	if oc == nil || oc.heartbeatWake == nil {
		return
	}
	oc.heartbeatWake.Request(reason, 0)
}

func (oc *AIClient) runHeartbeatImmediate(ctx context.Context, reason string) heartbeatRunResult {
	if oc == nil || oc.heartbeatRunner == nil {
		return heartbeatRunResult{Status: "skipped", Reason: "disabled"}
	}
	_ = ctx // currently no ctx plumbing in HeartbeatRunner
	return oc.heartbeatRunner.run(reason)
}

func (oc *AIClient) onCronEvent(evt cron.CronEvent) {
	if oc == nil || strings.TrimSpace(evt.JobID) == "" {
		return
	}
	oc.log.Debug().Str("job_id", evt.JobID).Str("action", evt.Action).Str("status", evt.Status).Int64("duration_ms", evt.DurationMs).Msg("Cron event received")
	if evt.Action != "finished" {
		return
	}
	storePath := resolveCronStorePath(&oc.connector.Config)
	path := cron.ResolveCronRunLogPath(storePath, evt.JobID)
	entry := cronRunLogEntryFromEvent(evt)
	backend := oc.bridgeStateBackend()
	if backend == nil {
		return
	}
	_ = cron.AppendCronRunLog(
		context.Background(),
		&cronStoreBackendAdapter{backend: &lazyStoreBackend{client: oc}},
		path,
		entry,
		0,
		0,
	)
}
