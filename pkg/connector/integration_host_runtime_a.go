package connector

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/agents"
	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
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

func (a *cronStoreBackendAdapter) List(ctx context.Context, prefix string) ([]integrationcron.StoreEntry, error) {
	if a == nil || a.backend == nil {
		return nil, errors.New("bridge state store not available")
	}
	entries, err := a.backend.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]integrationcron.StoreEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, integrationcron.StoreEntry{Key: entry.Key, Data: entry.Data})
	}
	return out, nil
}

func resolveCronEnabled(cfg *Config) bool {
	if cfg == nil || cfg.Cron == nil {
		return integrationcron.ResolveCronEnabled(nil)
	}
	return integrationcron.ResolveCronEnabled(cfg.Cron.Enabled)
}

func resolveCronStorePath(cfg *Config) string {
	raw := ""
	if cfg != nil && cfg.Cron != nil {
		raw = cfg.Cron.Store
	}
	return integrationcron.ResolveCronStorePath(raw)
}

func resolveCronMaxConcurrentRuns(cfg *Config) int {
	if cfg == nil || cfg.Cron == nil {
		return integrationcron.ResolveCronMaxConcurrentRuns(0)
	}
	return integrationcron.ResolveCronMaxConcurrentRuns(cfg.Cron.MaxConcurrentRuns)
}

func (oc *AIClient) buildCronService() *integrationcron.Service {
	if oc == nil {
		return nil
	}
	storePath := resolveCronStorePath(&oc.connector.Config)
	storeBackend := &cronStoreBackendAdapter{backend: &lazyStoreBackend{client: oc}}
	return integrationcron.BuildCronService(integrationcron.ServiceBuildDeps{
		NowMs:             func() int64 { return time.Now().UnixMilli() },
		Log:               oc.log,
		StorePath:         storePath,
		Store:             storeBackend,
		MaxConcurrentRuns: resolveCronMaxConcurrentRuns(&oc.connector.Config),
		CronEnabled:       resolveCronEnabled(&oc.connector.Config),
		ResolveJobTimeoutMs: func(job integrationcron.Job) int64 {
			return oc.resolveCronJobTimeoutMs(job)
		},
		EnqueueSystemEvent: func(ctx context.Context, text string, agentID string) error {
			return oc.enqueueCronSystemEvent(ctx, text, agentID)
		},
		RequestHeartbeatNow: func(ctx context.Context, reason string) {
			oc.requestHeartbeatNow(ctx, reason)
		},
		RunHeartbeatOnce: func(ctx context.Context, reason string) integrationcron.HeartbeatRunResult {
			res := oc.runHeartbeatImmediate(ctx, reason)
			return integrationcron.HeartbeatRunResult{Status: res.Status, Reason: res.Reason}
		},
		RunIsolatedAgentJob: func(ctx context.Context, job integrationcron.Job, message string) (string, string, string, error) {
			return oc.runCronIsolatedAgentJob(ctx, job, message)
		},
		OnEvent: oc.onCronEvent,
	})
}

func (oc *AIClient) resolveCronJobTimeoutMs(job integrationcron.Job) int64 {
	if oc == nil {
		return 0
	}
	defaultSeconds := 600
	if cfg := &oc.connector.Config; cfg != nil && cfg.Agents != nil && cfg.Agents.Defaults != nil && cfg.Agents.Defaults.TimeoutSeconds > 0 {
		defaultSeconds = cfg.Agents.Defaults.TimeoutSeconds
	}
	return integrationcron.ResolveCronJobTimeoutMs(job, defaultSeconds)
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
	_ = ctx
	return oc.heartbeatRunner.run(reason)
}

func (oc *AIClient) onCronEvent(evt integrationcron.Event) {
	if oc == nil {
		return
	}
	storePath := resolveCronStorePath(&oc.connector.Config)
	backend := &cronStoreBackendAdapter{backend: &lazyStoreBackend{client: oc}}
	integrationcron.HandleCronEvent(evt, integrationcron.EventLogDeps{
		StorePath: storePath,
		Log:       integrationcron.NewZeroLogger(oc.log),
		NowMs:     func() int64 { return time.Now().UnixMilli() },
		AppendRunLog: func(ctx context.Context, path string, entry integrationcron.RunLogEntry) error {
			return integrationcron.AppendRunLog(ctx, integrationcron.NewStoreBackendAdapter(backend), path, entry, 0, 0)
		},
	})
}

func resolveCronAgentID(raw string, cfg *Config) string {
	return integrationcron.ResolveCronAgentID(
		raw,
		agents.DefaultAgentID,
		normalizeAgentID,
		func(normalized string) bool {
			if cfg == nil || cfg.Agents == nil {
				return false
			}
			for _, entry := range cfg.Agents.List {
				if normalizeAgentID(entry.ID) == strings.TrimSpace(normalized) {
					return true
				}
			}
			return false
		},
	)
}

func cronSessionKey(agentID, jobID string) string {
	return integrationcron.CronSessionKey(agentID, jobID, normalizeAgentID)
}

func (oc *AIClient) updateCronSessionEntry(ctx context.Context, sessionKey string, updater func(entry integrationcron.SessionEntry) integrationcron.SessionEntry) {
	if oc == nil {
		return
	}
	integrationcron.UpdateSessionEntry(
		ctx,
		oc.bridgeStateBackend(),
		integrationcron.NewZeroLogger(oc.log),
		sessionKey,
		func(entry integrationcron.SessionEntry) integrationcron.SessionEntry {
			if updater == nil {
				return entry
			}
			return updater(entry)
		},
	)
}

func (oc *AIClient) readCronRuns(jobID string, limit int) ([]integrationcron.RunLogEntry, error) {
	if oc == nil {
		return nil, errors.New("cron service not available")
	}
	if known, available, _, reason := oc.integratedToolAvailability(&PortalMetadata{}, ToolNameCron); known && !available {
		if strings.TrimSpace(reason) == "" {
			reason = "cron service not available"
		}
		return nil, errors.New(reason)
	}
	if limit <= 0 {
		limit = 200
	}
	storePath := resolveCronStorePath(&oc.connector.Config)
	stateBackend := oc.bridgeStateBackend()
	if stateBackend == nil {
		return nil, errors.New("cron store not available")
	}
	cronBackend := &cronStoreBackendAdapter{backend: &lazyStoreBackend{client: oc}}
	trimmed := strings.TrimSpace(jobID)
	if trimmed != "" {
		path := integrationcron.ResolveRunLogPath(storePath, trimmed)
		return integrationcron.ReadRunLogEntries(context.Background(), integrationcron.NewStoreBackendAdapter(cronBackend), path, limit, trimmed)
	}
	entries := make([]integrationcron.RunLogEntry, 0)
	runDir := integrationcron.ResolveRunLogDir(storePath)
	storeEntries, err := stateBackend.List(context.Background(), runDir)
	if err != nil {
		return entries, nil
	}
	for _, se := range storeEntries {
		if !strings.HasSuffix(strings.ToLower(se.Key), ".jsonl") {
			continue
		}
		list := integrationcron.ParseRunLogEntries(string(se.Data), limit, "")
		if len(list) > 0 {
			entries = append(entries, list...)
		}
	}
	slices.SortFunc(entries, func(a, b integrationcron.RunLogEntry) int {
		return cmp.Compare(a.TS, b.TS)
	})
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}
