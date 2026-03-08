package cron

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	croncore "github.com/beeper/ai-bridge/pkg/cron"
	iruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

const moduleName = "cron"

// Integration is the self-owned cron integration module.
// It implements ToolIntegration, CommandIntegration, and LifecycleIntegration
// directly, wiring all deps from Host and optional capability interfaces.
type Integration struct {
	host      iruntime.Host
	service   *Service
	storePath string
}

func New(host iruntime.Host) iruntime.ModuleHooks {
	if host == nil {
		return nil
	}
	return &Integration{host: host}
}

func (i *Integration) Name() string { return moduleName }

// ---- LifecycleIntegration ----

func (i *Integration) Start(ctx context.Context) error {
	cfg := i.resolveCronConfig()
	i.storePath = cfg.storePath

	log := i.resolveZerologLogger()
	sb := i.host.StoreBackend()
	storeBackend := &runtimeStoreAdapter{sb: sb}

	i.service = BuildCronService(ServiceBuildDeps{
		NowMs:             func() int64 { return time.Now().UnixMilli() },
		Log:               log,
		StorePath:         cfg.storePath,
		Store:             storeBackend,
		MaxConcurrentRuns: cfg.maxConcurrentRuns,
		CronEnabled:       cfg.enabled,
		ResolveJobTimeoutMs: func(job Job) int64 {
			defaultSeconds := 600
			if ah, ok := i.host.(iruntime.AgentHelper); ok {
				if s := ah.AgentTimeoutSeconds(); s > 0 {
					defaultSeconds = s
				}
			}
			return ResolveCronJobTimeoutMs(job, defaultSeconds)
		},
		EnqueueSystemEvent: func(ctx context.Context, text string, agentID string) error {
			return i.enqueueCronSystemEvent(ctx, text, agentID)
		},
		RequestHeartbeatNow: func(ctx context.Context, reason string) {
			if hb := i.host.Heartbeat(); hb != nil {
				hb.RequestNow(ctx, reason)
			}
		},
		RunHeartbeatOnce: func(ctx context.Context, reason string) HeartbeatRunResult {
			hh, ok := i.host.(iruntime.HeartbeatHelper)
			if !ok {
				return HeartbeatRunResult{Status: "skipped", Reason: "disabled"}
			}
			status, reasonMsg := hh.RunHeartbeatOnce(ctx, reason)
			return HeartbeatRunResult{Status: status, Reason: reasonMsg}
		},
		RunIsolatedAgentJob: func(ctx context.Context, job Job, message string) (string, string, string, error) {
			return i.runIsolatedAgentJob(ctx, job, message)
		},
		OnEvent: func(evt Event) {
			i.onCronEvent(evt)
		},
	})

	if i.service == nil {
		return nil
	}
	return i.service.Start()
}

func (i *Integration) Stop() {
	if i.service != nil {
		i.service.Stop()
	}
}

// ---- ToolIntegration ----

func (i *Integration) ToolDefinitions(_ context.Context, _ iruntime.ToolScope) []iruntime.ToolDefinition {
	return []iruntime.ToolDefinition{{
		Name:        toolspec.CronName,
		Description: toolspec.CronDescription,
		Parameters:  toolspec.CronSchema(),
	}}
}

func (i *Integration) ExecuteTool(ctx context.Context, call iruntime.ToolCall) (bool, string, error) {
	if !strings.EqualFold(strings.TrimSpace(call.Name), toolspec.CronName) {
		return false, "", nil
	}
	result, err := ExecuteTool(ctx, call.Args, i.buildToolExecDeps(call.Scope))
	return true, result, err
}

func (i *Integration) ToolAvailability(_ context.Context, _ iruntime.ToolScope, toolName string) (bool, bool, iruntime.SettingSource, string) {
	if !strings.EqualFold(strings.TrimSpace(toolName), toolspec.CronName) {
		return false, false, iruntime.SourceGlobalDefault, ""
	}
	if i.service == nil {
		return true, false, iruntime.SourceProviderLimit, "Cron service not available"
	}
	return true, true, iruntime.SourceGlobalDefault, ""
}

// ---- CommandIntegration ----

func (i *Integration) CommandDefinitions(_ context.Context, _ iruntime.CommandScope) []iruntime.CommandDefinition {
	return []iruntime.CommandDefinition{{
		Name:           "cron",
		Description:    "Inspect/manage cron jobs",
		Args:           "[status|list|runs|run|remove] ...",
		RequiresPortal: true,
		RequiresLogin:  true,
	}}
}

func (i *Integration) ExecuteCommand(_ context.Context, call iruntime.CommandCall) (bool, error) {
	if strings.ToLower(strings.TrimSpace(call.Name)) != moduleName {
		return false, nil
	}
	return i.executeCronCommand(call)
}

// ---- private: command handler ----

func (i *Integration) executeCronCommand(call iruntime.CommandCall) (bool, error) {
	reply := call.Reply
	if reply == nil {
		reply = func(string, ...any) {}
	}
	if i.service == nil {
		reply("Cron service not available.")
		return true, nil
	}
	action := "status"
	if len(call.Args) > 0 {
		action = strings.ToLower(strings.TrimSpace(call.Args[0]))
	}
	switch action {
	case "status":
		enabled, storePath, jobCount, nextWake, err := i.service.Status()
		if err != nil {
			reply("Cron status failed: %s", err.Error())
			return true, nil
		}
		reply(formatCronStatusText(enabled, storePath, jobCount, nextWake))
	case "list":
		includeDisabled := false
		if len(call.Args) > 1 && (strings.EqualFold(call.Args[1], "all") || strings.EqualFold(call.Args[1], "--all")) {
			includeDisabled = true
		}
		jobs, err := i.service.List(includeDisabled)
		if err != nil {
			reply("Cron list failed: %s", err.Error())
			return true, nil
		}
		reply(formatCronJobListText(jobs))
	case "runs":
		if len(call.Args) < 2 || strings.TrimSpace(call.Args[1]) == "" {
			reply("Usage: `!ai cron runs <jobId> [limit]`")
			return true, nil
		}
		jobID := strings.TrimSpace(call.Args[1])
		limit := 50
		if len(call.Args) > 2 && strings.TrimSpace(call.Args[2]) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(call.Args[2])); err == nil && n > 0 {
				limit = n
			}
		}
		entries, err := i.readRuns(jobID, limit)
		if err != nil {
			reply("Cron runs failed: %s", err.Error())
			return true, nil
		}
		reply(formatCronRunsText(jobID, entries))
	case "remove", "rm", "delete":
		if len(call.Args) < 2 || strings.TrimSpace(call.Args[1]) == "" {
			reply("Usage: `!ai cron remove <jobId>`")
			return true, nil
		}
		jobID := strings.TrimSpace(call.Args[1])
		removed, err := i.service.Remove(jobID)
		if err != nil {
			reply("Cron remove failed: %s", err.Error())
			return true, nil
		}
		if removed {
			reply("Removed.")
		} else {
			reply("No such job (already removed?).")
		}
	case "run":
		if len(call.Args) < 2 || strings.TrimSpace(call.Args[1]) == "" {
			reply("Usage: `!ai cron run <jobId> [force]`")
			return true, nil
		}
		jobID := strings.TrimSpace(call.Args[1])
		mode := ""
		if len(call.Args) > 2 && strings.EqualFold(strings.TrimSpace(call.Args[2]), "force") {
			mode = "force"
		}
		ran, reason, err := i.service.Run(jobID, mode)
		if err != nil {
			reply("Cron run failed: %s", err.Error())
			return true, nil
		}
		if ran {
			reply("Triggered.")
			return true, nil
		}
		if strings.TrimSpace(reason) == "" {
			reason = "not-due"
		}
		reply("Not run (%s).", reason)
	default:
		reply("Usage:\n- `!ai cron status`\n- `!ai cron list [all]`\n- `!ai cron runs <jobId> [limit]`\n- `!ai cron run <jobId> [force]`\n- `!ai cron remove <jobId>`")
	}
	return true, nil
}

// ---- private: tool deps wiring ----

var errCronServiceNotAvailable = errors.New("cron service not available")

func (i *Integration) buildToolExecDeps(scope iruntime.ToolScope) ToolExecDeps {
	svc := i.service
	requireSvc := func() error {
		if svc == nil {
			return errCronServiceNotAvailable
		}
		return nil
	}
	return ToolExecDeps{
		Status: func() (bool, string, int, *int64, error) {
			if err := requireSvc(); err != nil {
				return false, "", 0, nil, err
			}
			return svc.Status()
		},
		List: func(includeDisabled bool) ([]Job, error) {
			if err := requireSvc(); err != nil {
				return nil, err
			}
			return svc.List(includeDisabled)
		},
		Add: func(input JobCreate) (Job, error) {
			if err := requireSvc(); err != nil {
				return Job{}, err
			}
			return svc.Add(input)
		},
		Update: func(id string, patch JobPatch) (Job, error) {
			if err := requireSvc(); err != nil {
				return Job{}, err
			}
			return svc.Update(id, patch)
		},
		Remove: func(id string) (bool, error) {
			if err := requireSvc(); err != nil {
				return false, err
			}
			return svc.Remove(id)
		},
		Run: func(id string, mode string) (bool, string, error) {
			if err := requireSvc(); err != nil {
				return false, "", err
			}
			return svc.Run(id, mode)
		},
		Runs: func(jobID string, limit int) ([]RunLogEntry, error) {
			return i.readRuns(jobID, limit)
		},
		Wake: func(mode string, text string) (bool, error) {
			if err := requireSvc(); err != nil {
				return false, err
			}
			return svc.Wake(mode, text)
		},
		NowMs:                func() int64 { return time.Now().UnixMilli() },
		ResolveCreateContext: func() ToolCreateContext { return i.resolveToolCreateContext(scope) },
		ResolveReminderLines: func(count int) []ReminderContextLine { return i.resolveReminderLines(scope, count) },
		ValidateDeliveryTo:   ValidateDeliveryTo,
	}
}

func (i *Integration) resolveToolCreateContext(scope iruntime.ToolScope) ToolCreateContext {
	tc := ToolCreateContext{}
	if ma, ok := i.host.(iruntime.MetadataAccess); ok && scope.Meta != nil {
		tc.AgentID = ma.AgentIDFromMeta(scope.Meta)
		tc.SourceInternal = ma.IsInternalRoom(scope.Meta)
	}
	if pm, ok := i.host.(iruntime.PortalManager); ok && scope.Portal != nil {
		tc.SourceRoomID = pm.PortalRoomID(scope.Portal)
	}
	return tc
}

func (i *Integration) resolveReminderLines(scope iruntime.ToolScope, count int) []ReminderContextLine {
	mh, ok := i.host.(iruntime.MessageHelper)
	if !ok || scope.Portal == nil || count <= 0 {
		return nil
	}
	summaries := mh.RecentMessages(context.Background(), scope.Portal, count)
	out := make([]ReminderContextLine, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, ReminderContextLine{Role: s.Role, Text: s.Body})
	}
	return out
}

// ---- private: isolated agent job ----

func (i *Integration) runIsolatedAgentJob(ctx context.Context, job Job, message string) (string, string, string, error) {
	return RunCronIsolatedAgentJob(ctx, job, message, i.buildIsolatedRunnerDeps())
}

func (i *Integration) buildIsolatedRunnerDeps() IsolatedRunnerDeps {
	return IsolatedRunnerDeps{
		DeliveryTimeout: DefaultCronDeliveryTimeout,
		MergeContext: func(ctx context.Context) (context.Context, context.CancelFunc) {
			if ch, ok := i.host.(iruntime.ContextHelper); ok {
				return ch.MergeDisconnectContext(ctx)
			}
			return context.WithCancel(ctx)
		},
		ResolveAgentID: func(raw string) string {
			ah, ok := i.host.(iruntime.AgentHelper)
			if !ok {
				return strings.TrimSpace(raw)
			}
			return ah.ResolveAgentID(raw, ah.DefaultAgentID())
		},
		GetOrCreateRoom: func(ctx context.Context, agentID, jobID, jobName string) (any, error) {
			pm, ok := i.host.(iruntime.PortalManager)
			if !ok {
				return nil, errors.New("portal manager not available")
			}
			return i.getOrCreateCronRoom(ctx, pm, agentID, jobID, jobName)
		},
		BuildDispatchMetadata: func(room any, patch MetadataPatch) any {
			ma, ok := i.host.(iruntime.MetadataAccess)
			if !ok {
				return nil
			}
			meta := ma.CloneMeta(room)
			if meta == nil {
				return nil
			}
			ma.SetMetaField(meta, "AgentID", patch.AgentID)
			if patch.Model != nil {
				ma.SetMetaField(meta, "Model", strings.TrimSpace(*patch.Model))
			}
			if patch.ReasoningEffort != nil {
				ma.SetMetaField(meta, "ReasoningEffort", strings.TrimSpace(*patch.ReasoningEffort))
			}
			if patch.DisableMessageTool {
				ma.SetMetaField(meta, "DisabledTools", []string{"message"})
			}
			return meta
		},
		NormalizeThinkingLevel: func(raw string) (string, bool) {
			ah, ok := i.host.(iruntime.AgentHelper)
			if !ok {
				return "", false
			}
			return ah.NormalizeThinkingLevel(raw)
		},
		SessionKey: func(agentID, jobID string) string {
			ah, ok := i.host.(iruntime.AgentHelper)
			if !ok {
				return CronSessionKey(agentID, jobID, nil)
			}
			return CronSessionKey(agentID, jobID, ah.NormalizeAgentID)
		},
		UpdateSessionEntry: func(ctx context.Context, sessionKey string, updater func(entry SessionEntry) SessionEntry) {
			sb := i.host.StoreBackend()
			if sb == nil {
				return
			}
			UpdateSessionEntry(ctx, &runtimeStoreAdapter{sb: sb}, i.resolveCoreLogger(), sessionKey, updater)
		},
		ResolveUserTimezone: func() string {
			ah, ok := i.host.(iruntime.AgentHelper)
			if !ok {
				return ""
			}
			tz, _ := ah.UserTimezone()
			return tz
		},
		LastAssistantMessage: func(ctx context.Context, room any) (string, int64) {
			mh, ok := i.host.(iruntime.MessageHelper)
			if !ok {
				return "", 0
			}
			return mh.LastAssistantMessage(ctx, room)
		},
		DispatchInternalMessage: func(ctx context.Context, room any, metadata any, message string) error {
			d := i.host.Dispatch()
			if d == nil {
				return errors.New("dispatch not available")
			}
			return d.DispatchInternalMessage(ctx, room, metadata, message, "cron")
		},
		WaitForAssistantMessage: func(ctx context.Context, room any, lastID string, lastTimestamp int64) (AssistantMessage, bool) {
			mh, ok := i.host.(iruntime.MessageHelper)
			if !ok {
				return AssistantMessage{}, false
			}
			info, found := mh.WaitForAssistantMessage(ctx, room, lastID, lastTimestamp)
			if !found {
				return AssistantMessage{}, false
			}
			return AssistantMessage{
				Body:             info.Body,
				Model:            info.Model,
				PromptTokens:     info.PromptTokens,
				CompletionTokens: info.CompletionTokens,
			}, true
		},
		ResolveAckMaxChars: func(agentID string) int {
			hh, ok := i.host.(iruntime.HeartbeatHelper)
			if !ok {
				return 0
			}
			return hh.HeartbeatAckMaxChars(agentID)
		},
		ResolveDeliveryTarget: func(agentID string, delivery *Delivery) DeliveryTarget {
			return i.resolveDeliveryTarget(agentID, delivery)
		},
		SendDeliveryMessage: func(ctx context.Context, portal any, body string) error {
			d := i.host.Dispatch()
			if d == nil {
				return errors.New("dispatch not available")
			}
			return d.SendAssistantMessage(ctx, portal, body)
		},
	}
}

// ---- private: delivery target resolution ----

func (i *Integration) resolveDeliveryTarget(agentID string, delivery *Delivery) DeliveryTarget {
	pr := i.host.PortalResolver()
	return ResolveCronDeliveryTarget(agentID, delivery, DeliveryResolverDeps{
		ResolveLastTarget: func(agentID string) (string, string, bool) {
			hh, ok := i.host.(iruntime.HeartbeatHelper)
			if !ok {
				return "", "", false
			}
			return hh.ResolveLastTarget(agentID)
		},
		IsStaleTarget: func(roomID, agentID string) bool {
			if pr == nil {
				return false
			}
			portal := pr.ResolvePortalByRoomID(context.Background(), roomID)
			if portal == nil {
				return false
			}
			ma, ok := i.host.(iruntime.MetadataAccess)
			if !ok {
				return false
			}
			ah, ok := i.host.(iruntime.AgentHelper)
			if !ok {
				return false
			}
			portalMeta := ma.PortalMeta(portal)
			return ah.NormalizeAgentID(ma.AgentIDFromMeta(portalMeta)) != ah.NormalizeAgentID(agentID)
		},
		LastActiveRoomID: func(agentID string) string {
			if pr == nil {
				return ""
			}
			portal := pr.ResolveLastActivePortal(context.Background(), agentID)
			if portal == nil {
				return ""
			}
			pm, ok := i.host.(iruntime.PortalManager)
			if !ok {
				return ""
			}
			return pm.PortalRoomID(portal)
		},
		DefaultChatRoomID: func() string {
			if pr == nil {
				return ""
			}
			portal := pr.ResolveDefaultPortal(context.Background())
			if portal == nil {
				return ""
			}
			pm, ok := i.host.(iruntime.PortalManager)
			if !ok {
				return ""
			}
			return pm.PortalRoomID(portal)
		},
		ResolvePortalByRoom: func(roomID string) any {
			if pr == nil {
				return nil
			}
			return pr.ResolvePortalByRoomID(context.Background(), roomID)
		},
		IsLoggedIn: func() bool {
			lh, ok := i.host.(iruntime.LoginHelper)
			if !ok {
				return false
			}
			return lh.IsLoggedIn()
		},
	})
}

// ---- private: cron room management ----

func (i *Integration) getOrCreateCronRoom(ctx context.Context, pm iruntime.PortalManager, agentID, jobID, jobName string) (any, error) {
	ah, _ := i.host.(iruntime.AgentHelper)
	defaultAgent := "default"
	if ah != nil {
		defaultAgent = ah.DefaultAgentID()
	}
	return GetOrCreateCronRoom(ctx, agentID, jobID, jobName, RoomResolverDeps{
		DefaultAgentID: defaultAgent,
		ResolveRoom: func(ctx context.Context, normalizedAgentID, normalizedJobID string) (any, string, error) {
			portalID := cronPortalID(normalizedAgentID, normalizedJobID)
			portal, roomID, err := pm.GetOrCreatePortal(ctx, portalID, "", "", nil)
			return portal, roomID, err
		},
		CreateRoom: func(ctx context.Context, normalizedAgentID, normalizedJobID, displayName string) (any, error) {
			portalID := cronPortalID(normalizedAgentID, normalizedJobID)
			portal, _, err := pm.GetOrCreatePortal(ctx, portalID, "", displayName, func(meta any) {
				ma, ok := i.host.(iruntime.MetadataAccess)
				if !ok {
					return
				}
				ma.SetModuleMeta(meta, "cron", map[string]any{
					"is_internal_room": true,
					"cron_job_id":      normalizedJobID,
				})
				ma.SetMetaField(meta, "AgentID", normalizedAgentID)
			})
			return portal, err
		},
		LogCreated: func(_ context.Context, agentID, jobID string, _ any) {
			i.host.Logger().Info("Created cron room", map[string]any{
				"agent_id": strings.TrimSpace(agentID),
				"job_id":   strings.TrimSpace(jobID),
			})
		},
	})
}

func cronPortalID(agentID, jobID string) string {
	return fmt.Sprintf("cron:%s:%s", agentID, jobID)
}

// ---- private: system events ----

func (i *Integration) enqueueCronSystemEvent(_ context.Context, text string, agentID string) error {
	hh, ok := i.host.(iruntime.HeartbeatHelper)
	if !ok {
		return nil
	}
	ah, _ := i.host.(iruntime.AgentHelper)
	if ah != nil {
		agentID = ah.ResolveAgentID(agentID, ah.DefaultAgentID())
	}
	_, sessionKey, err := hh.ResolveHeartbeatSessionPortal(agentID)
	if err != nil || sessionKey == "" {
		sessionKey = hh.ResolveHeartbeatSessionKey(agentID)
		if sessionKey == "" {
			return nil
		}
	}
	hh.EnqueueSystemEvent(sessionKey, text, agentID)
	hh.PersistSystemEvents()
	return nil
}

// ---- private: event logging ----

func (i *Integration) onCronEvent(evt Event) {
	sb := i.host.StoreBackend()
	if sb == nil {
		return
	}
	backend := &runtimeStoreAdapter{sb: sb}
	HandleCronEvent(evt, EventLogDeps{
		StorePath: i.storePath,
		Log:       i.resolveCoreLogger(),
		NowMs:     func() int64 { return time.Now().UnixMilli() },
		AppendRunLog: func(ctx context.Context, path string, entry RunLogEntry) error {
			return AppendRunLog(ctx, NewStoreBackendAdapter(backend), path, entry, 0, 0)
		},
	})
}

// ---- private: run log reading ----

func (i *Integration) readRuns(jobID string, limit int) ([]RunLogEntry, error) {
	sb := i.host.StoreBackend()
	if sb == nil {
		return nil, errors.New("cron store not available")
	}
	if limit <= 0 {
		limit = 200
	}
	backend := &runtimeStoreAdapter{sb: sb}
	trimmed := strings.TrimSpace(jobID)
	if trimmed != "" {
		path := ResolveRunLogPath(i.storePath, trimmed)
		return ReadRunLogEntries(context.Background(), NewStoreBackendAdapter(backend), path, limit, trimmed)
	}
	entries := make([]RunLogEntry, 0)
	runDir := ResolveRunLogDir(i.storePath)
	storeEntries, err := sb.List(context.Background(), runDir)
	if err != nil {
		return entries, fmt.Errorf("list run logs: %w", err)
	}
	for _, se := range storeEntries {
		if !strings.HasSuffix(strings.ToLower(se.Key), ".jsonl") {
			continue
		}
		list := ParseRunLogEntries(string(se.Data), limit, "")
		if len(list) > 0 {
			entries = append(entries, list...)
		}
	}
	slices.SortFunc(entries, func(a, b RunLogEntry) int {
		return cmp.Compare(a.TS, b.TS)
	})
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// ---- private: config/logging helpers ----

type cronConfig struct {
	enabled           bool
	storePath         string
	maxConcurrentRuns int
}

func (i *Integration) resolveCronConfig() cronConfig {
	cfg := cronConfig{enabled: true, maxConcurrentRuns: 1}
	cl := i.host.ConfigLookup()
	if cl == nil {
		cfg.storePath = ResolveCronStorePath("")
		return cfg
	}
	cfg.enabled = cl.ModuleEnabled(moduleName)
	raw := cl.ModuleConfig(moduleName)
	if raw != nil {
		if store, ok := raw["store"].(string); ok {
			cfg.storePath = store
		}
		switch v := raw["max_concurrent_runs"].(type) {
		case int:
			cfg.maxConcurrentRuns = v
		case float64:
			cfg.maxConcurrentRuns = int(v)
		}
	}
	cfg.storePath = ResolveCronStorePath(cfg.storePath)
	cfg.maxConcurrentRuns = ResolveCronMaxConcurrentRuns(cfg.maxConcurrentRuns)
	return cfg
}

func (i *Integration) resolveZerologLogger() zerolog.Logger {
	return iruntime.ZerologFromHost(i.host)
}

func (i *Integration) resolveCoreLogger() croncore.Logger {
	return NewZeroLogger(i.resolveZerologLogger())
}

// ---- private: runtime store adapter ----

// runtimeStoreAdapter adapts iruntime.StoreBackend to the cron StoreBackend interface.
type runtimeStoreAdapter struct {
	sb iruntime.StoreBackend
}

func (a *runtimeStoreAdapter) Read(ctx context.Context, key string) ([]byte, bool, error) {
	if a == nil || a.sb == nil {
		return nil, false, errors.New("bridge state store not available")
	}
	return a.sb.Read(ctx, key)
}

func (a *runtimeStoreAdapter) Write(ctx context.Context, key string, data []byte) error {
	if a == nil || a.sb == nil {
		return errors.New("bridge state store not available")
	}
	return a.sb.Write(ctx, key, data)
}

func (a *runtimeStoreAdapter) List(ctx context.Context, prefix string) ([]StoreEntry, error) {
	if a == nil || a.sb == nil {
		return nil, errors.New("bridge state store not available")
	}
	entries, err := a.sb.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]StoreEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, StoreEntry{Key: e.Key, Data: e.Data})
	}
	return out, nil
}
