package connector

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/cron"
)

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
	storeBackend := &lazyStoreBackend{client: oc}
	deps := cron.CronServiceDeps{
		NowMs:               func() int64 { return time.Now().UnixMilli() },
		Log:                 cronLogger{log: oc.log},
		StorePath:           storePath,
		Store:               storeBackend,
		MaxConcurrentRuns:   resolveCronMaxConcurrentRuns(&oc.connector.Config),
		CronEnabled:         resolveCronEnabled(&oc.connector.Config),
		EnqueueSystemEvent:  oc.enqueueCronSystemEvent,
		RequestHeartbeatNow: oc.requestHeartbeatNow,
		RunHeartbeatOnce:    oc.runHeartbeatImmediate,
		RunIsolatedAgentJob: oc.runCronIsolatedAgentJob,
		OnEvent:             oc.onCronEvent,
	}
	return cron.NewCronService(deps)
}

func (oc *AIClient) enqueueCronSystemEvent(text string, agentID string) error {
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

func (oc *AIClient) requestHeartbeatNow(reason string) {
	if oc == nil || oc.heartbeatWake == nil {
		return
	}
	oc.heartbeatWake.Request(reason, 0)
}

func (oc *AIClient) runHeartbeatImmediate(reason string) cron.HeartbeatRunResult {
	if oc == nil || oc.heartbeatRunner == nil {
		return cron.HeartbeatRunResult{Status: "skipped", Reason: "disabled"}
	}
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
	_ = cron.AppendCronRunLog(context.Background(), backend, path, entry, 0, 0)
}
