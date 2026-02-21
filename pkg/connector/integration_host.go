package connector

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"

	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
	integrationmemory "github.com/beeper/ai-bridge/pkg/integrations/memory"
	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
)

type runtimeIntegrationHost struct {
	client      *AIClient
	cronService *integrationcron.Service
}

func newRuntimeIntegrationHost(client *AIClient) *runtimeIntegrationHost {
	h := &runtimeIntegrationHost{client: client}
	if client != nil {
		h.cronService = client.buildCronService()
	}
	return h
}

func (h *runtimeIntegrationHost) Logger() integrationruntime.Logger {
	return &runtimeLogger{client: h.client}
}
func (h *runtimeIntegrationHost) Now() time.Time                                    { return time.Now() }
func (h *runtimeIntegrationHost) StoreBackend() integrationruntime.StoreBackend     { return nil }
func (h *runtimeIntegrationHost) PortalResolver() integrationruntime.PortalResolver { return nil }
func (h *runtimeIntegrationHost) Dispatch() integrationruntime.Dispatch             { return nil }
func (h *runtimeIntegrationHost) SessionStore() integrationruntime.SessionStore     { return nil }
func (h *runtimeIntegrationHost) Heartbeat() integrationruntime.Heartbeat           { return nil }
func (h *runtimeIntegrationHost) ToolExec() integrationruntime.ToolExec             { return nil }
func (h *runtimeIntegrationHost) PromptContext() integrationruntime.PromptContext   { return nil }
func (h *runtimeIntegrationHost) DBAccess() integrationruntime.DBAccess             { return nil }
func (h *runtimeIntegrationHost) ConfigLookup() integrationruntime.ConfigLookup     { return h }

func (h *runtimeIntegrationHost) ModuleEnabled(name string) bool {
	if h == nil || h.client == nil || h.client.connector == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "cron":
		if h.client.connector.Config.Integrations == nil || h.client.connector.Config.Integrations.Cron == nil {
			return true
		}
		return *h.client.connector.Config.Integrations.Cron
	case "memory":
		if h.client.connector.Config.Integrations == nil || h.client.connector.Config.Integrations.Memory == nil {
			return true
		}
		return *h.client.connector.Config.Integrations.Memory
	default:
		return true
	}
}

// cron host implementation
func (h *runtimeIntegrationHost) ToolDefinitions(_ context.Context, _ integrationruntime.ToolScope) []integrationruntime.ToolDefinition {
	var out []integrationruntime.ToolDefinition
	if def, ok := integrationToolByName(ToolNameCron); ok {
		out = append(out, def)
	}
	if def, ok := integrationToolByName(ToolNameMemorySearch); ok {
		out = append(out, def)
	}
	if def, ok := integrationToolByName(ToolNameMemoryGet); ok {
		out = append(out, def)
	}
	return out
}

func (h *runtimeIntegrationHost) ExecuteTool(ctx context.Context, call integrationruntime.ToolCall) (bool, string, error) {
	switch call.Name {
	case ToolNameCron:
		result, err := executeCron(ctx, call.Args)
		return true, result, err
	case ToolNameMemorySearch:
		result, err := executeMemorySearch(ctx, call.Args)
		return true, result, err
	case ToolNameMemoryGet:
		result, err := executeMemoryGet(ctx, call.Args)
		return true, result, err
	default:
		return false, "", nil
	}
}

func (h *runtimeIntegrationHost) ToolAvailability(_ context.Context, scope integrationruntime.ToolScope, toolName string) (bool, bool, integrationruntime.SettingSource, string) {
	if h == nil || h.client == nil {
		switch toolName {
		case ToolNameCron, ToolNameMemorySearch, ToolNameMemoryGet:
			return true, false, integrationruntime.SourceProviderLimit, "integration unavailable"
		default:
			return false, false, integrationruntime.SourceGlobalDefault, ""
		}
	}
	switch toolName {
	case ToolNameCron:
		if h.cronService == nil {
			return true, false, integrationruntime.SourceProviderLimit, "Cron service not available"
		}
		return true, true, integrationruntime.SourceGlobalDefault, ""
	case ToolNameMemorySearch, ToolNameMemoryGet:
		meta, _ := scope.Meta.(*PortalMetadata)
		disabled, reason := h.client.isMemorySearchExplicitlyDisabled(meta)
		if disabled {
			return true, false, integrationruntime.SourceProviderLimit, reason
		}
		return true, true, integrationruntime.SourceGlobalDefault, ""
	default:
		return false, false, integrationruntime.SourceGlobalDefault, ""
	}
}

func (h *runtimeIntegrationHost) Start(_ context.Context) error {
	if h == nil || h.cronService == nil {
		return nil
	}
	return h.cronService.Start()
}

func (h *runtimeIntegrationHost) Stop() {
	if h == nil || h.cronService == nil {
		return
	}
	h.cronService.Stop()
}

func (h *runtimeIntegrationHost) Status() (bool, string, int, *int64, error) {
	if h == nil || h.cronService == nil {
		return false, "", 0, nil, errors.New("cron service not available")
	}
	return h.cronService.Status()
}

func (h *runtimeIntegrationHost) List(includeDisabled bool) ([]integrationcron.Job, error) {
	if h == nil || h.cronService == nil {
		return nil, errors.New("cron service not available")
	}
	return h.cronService.List(includeDisabled)
}

func (h *runtimeIntegrationHost) Add(input integrationcron.JobCreate) (integrationcron.Job, error) {
	if h == nil || h.cronService == nil {
		return integrationcron.Job{}, errors.New("cron service not available")
	}
	return h.cronService.Add(input)
}

func (h *runtimeIntegrationHost) Update(id string, patch integrationcron.JobPatch) (integrationcron.Job, error) {
	if h == nil || h.cronService == nil {
		return integrationcron.Job{}, errors.New("cron service not available")
	}
	return h.cronService.Update(id, patch)
}

func (h *runtimeIntegrationHost) Remove(id string) (bool, error) {
	if h == nil || h.cronService == nil {
		return false, errors.New("cron service not available")
	}
	return h.cronService.Remove(id)
}

func (h *runtimeIntegrationHost) Run(id string, mode string) (bool, string, error) {
	if h == nil || h.cronService == nil {
		return false, "", errors.New("cron service not available")
	}
	return h.cronService.Run(id, mode)
}

func (h *runtimeIntegrationHost) Wake(mode string, text string) (bool, error) {
	if h == nil || h.cronService == nil {
		return false, errors.New("cron service not available")
	}
	return h.cronService.Wake(mode, text)
}

func (h *runtimeIntegrationHost) Runs(jobID string, limit int) ([]integrationcron.RunLogEntry, error) {
	if h == nil || h.client == nil {
		return nil, errors.New("cron service not available")
	}
	return h.client.readCronRuns(jobID, limit)
}

// memory host implementation
func (h *runtimeIntegrationHost) AdditionalSystemMessages(_ context.Context, _ integrationruntime.PromptScope) []openai.ChatCompletionMessageParamUnion {
	return nil
}

func (h *runtimeIntegrationHost) AugmentPrompt(
	ctx context.Context,
	scope integrationruntime.PromptScope,
	prompt []openai.ChatCompletionMessageParamUnion,
) []openai.ChatCompletionMessageParamUnion {
	if h == nil || h.client == nil {
		return prompt
	}
	portal, _ := scope.Portal.(*bridgev2.Portal)
	meta, _ := scope.Meta.(*PortalMetadata)
	return h.client.injectMemoryContext(ctx, portal, meta, prompt)
}

func (h *runtimeIntegrationHost) GetManager(scope integrationruntime.ToolScope) (integrationmemory.Manager, string) {
	if h == nil || h.client == nil {
		return nil, "memory search unavailable"
	}
	meta, _ := scope.Meta.(*PortalMetadata)
	manager, errMsg := h.client.getMemoryManager(resolveAgentID(meta))
	if manager == nil {
		return nil, errMsg
	}
	return &memoryManagerAdapter{manager: manager}, ""
}

func (h *runtimeIntegrationHost) CommandDefinitions(_ context.Context, _ integrationruntime.CommandScope) []integrationruntime.CommandDefinition {
	return []integrationruntime.CommandDefinition{{
		Name:           "memory",
		Description:    "Inspect and edit memory files/index",
		Args:           "<status|reindex|search|get|set|append> [...]",
		RequiresPortal: true,
		RequiresLogin:  true,
		AdminOnly:      true,
	}}
}

func (h *runtimeIntegrationHost) ExecuteCommand(ctx context.Context, call integrationruntime.CommandCall) (bool, error) {
	if strings.ToLower(strings.TrimSpace(call.Name)) != "memory" {
		return false, nil
	}
	return executeMemoryCommand(ctx, call)
}

func (h *runtimeIntegrationHost) OnSessionMutation(ctx context.Context, evt integrationruntime.SessionMutationEvent) {
	if h == nil || h.client == nil {
		return
	}
	portal, _ := evt.Portal.(*bridgev2.Portal)
	meta, _ := evt.Meta.(*PortalMetadata)
	h.client.notifySessionMutation(ctx, portal, meta, evt.Force)
}

func (h *runtimeIntegrationHost) OnFileChanged(ctx context.Context, evt integrationruntime.FileChangedEvent) {
	notifyIntegrationFileChanged(ctx, evt.Path)
}

func (h *runtimeIntegrationHost) OnContextOverflow(
	ctx context.Context,
	call integrationruntime.ContextOverflowCall,
) (bool, []openai.ChatCompletionMessageParamUnion, error) {
	if h == nil || h.client == nil {
		return false, nil, nil
	}
	portal, _ := call.Portal.(*bridgev2.Portal)
	meta, _ := call.Meta.(*PortalMetadata)
	h.client.maybeRunMemoryFlush(ctx, portal, meta, call.Prompt)
	return false, nil, nil
}

func (h *runtimeIntegrationHost) StopForLogin(bridgeID, loginID string) {
	integrationmemory.StopManagersForLogin(bridgeID, loginID)
}

func (h *runtimeIntegrationHost) PurgeForLogin(ctx context.Context, scope integrationruntime.LoginScope) error {
	login, _ := scope.Login.(*bridgev2.UserLogin)
	if login == nil {
		return nil
	}
	db := bridgeDBFromLogin(login)
	chunkIDsByAgent := loadMemoryChunkIDsByAgentBestEffort(ctx, db, scope.BridgeID, scope.LoginID)
	integrationmemory.PurgeManagersForLogin(ctx, scope.BridgeID, scope.LoginID, chunkIDsByAgent)
	return nil
}

type memoryManagerAdapter struct {
	manager *integrationmemory.MemorySearchManager
}

func (m *memoryManagerAdapter) Status() integrationmemory.ProviderStatus {
	if m == nil || m.manager == nil {
		return integrationmemory.ProviderStatus{}
	}
	return m.manager.Status()
}

func (m *memoryManagerAdapter) Search(ctx context.Context, query string, opts integrationmemory.SearchOptions) ([]integrationmemory.SearchResult, error) {
	if m == nil || m.manager == nil {
		return nil, errors.New("memory search unavailable")
	}
	return m.manager.Search(ctx, query, opts)
}

func (m *memoryManagerAdapter) ReadFile(ctx context.Context, relPath string, from, lines *int) (map[string]any, error) {
	if m == nil || m.manager == nil {
		return nil, errors.New("memory search unavailable")
	}
	return m.manager.ReadFile(ctx, relPath, from, lines)
}

func (m *memoryManagerAdapter) StatusDetails(ctx context.Context) (*integrationmemory.StatusDetails, error) {
	if m == nil || m.manager == nil {
		return nil, errors.New("memory search unavailable")
	}
	status, err := m.manager.StatusDetails(ctx)
	if err != nil {
		return nil, err
	}
	var sourceCounts []integrationmemory.SourceCount
	if len(status.SourceCounts) > 0 {
		sourceCounts = make([]integrationmemory.SourceCount, 0, len(status.SourceCounts))
		for _, src := range status.SourceCounts {
			sourceCounts = append(sourceCounts, integrationmemory.SourceCount{Source: src.Source, Files: src.Files, Chunks: src.Chunks})
		}
	}
	var fallback *integrationmemory.FallbackStatus
	if status.Fallback != nil {
		fallback = &integrationmemory.FallbackStatus{From: status.Fallback.From, Reason: status.Fallback.Reason}
	}
	var cache *integrationmemory.CacheStatus
	if status.Cache != nil {
		cache = &integrationmemory.CacheStatus{Enabled: status.Cache.Enabled, Entries: status.Cache.Entries, MaxEntries: status.Cache.MaxEntries}
	}
	var fts *integrationmemory.FTSStatus
	if status.FTS != nil {
		fts = &integrationmemory.FTSStatus{Enabled: status.FTS.Enabled, Available: status.FTS.Available, Error: status.FTS.Error}
	}
	var vector *integrationmemory.VectorStatus
	if status.Vector != nil {
		vector = &integrationmemory.VectorStatus{
			Enabled:       status.Vector.Enabled,
			Available:     status.Vector.Available,
			ExtensionPath: status.Vector.ExtensionPath,
			LoadError:     status.Vector.LoadError,
			Dims:          status.Vector.Dims,
		}
	}
	var batch *integrationmemory.BatchStatus
	if status.Batch != nil {
		batch = &integrationmemory.BatchStatus{
			Enabled:        status.Batch.Enabled,
			Failures:       status.Batch.Failures,
			Limit:          status.Batch.Limit,
			Wait:           status.Batch.Wait,
			Concurrency:    status.Batch.Concurrency,
			PollIntervalMs: status.Batch.PollIntervalMs,
			TimeoutMs:      status.Batch.TimeoutMs,
			LastError:      status.Batch.LastError,
			LastProvider:   status.Batch.LastProvider,
		}
	}
	return &integrationmemory.StatusDetails{
		Files:             status.Files,
		Chunks:            status.Chunks,
		Dirty:             status.Dirty,
		WorkspaceDir:      status.WorkspaceDir,
		DBPath:            status.DBPath,
		Provider:          status.Provider,
		Model:             status.Model,
		RequestedProvider: status.RequestedProvider,
		Sources:           status.Sources,
		ExtraPaths:        status.ExtraPaths,
		SourceCounts:      sourceCounts,
		Cache:             cache,
		FTS:               fts,
		Fallback:          fallback,
		Vector:            vector,
		Batch:             batch,
	}, nil
}

func (m *memoryManagerAdapter) ProbeVectorAvailability(ctx context.Context) bool {
	if m == nil || m.manager == nil {
		return false
	}
	return m.manager.ProbeVectorAvailability(ctx)
}

func (m *memoryManagerAdapter) ProbeEmbeddingAvailability(ctx context.Context) (bool, string) {
	if m == nil || m.manager == nil {
		return false, "memory search unavailable"
	}
	return m.manager.ProbeEmbeddingAvailability(ctx)
}

func (m *memoryManagerAdapter) SyncWithProgress(ctx context.Context, onProgress func(completed, total int, label string)) error {
	if m == nil || m.manager == nil {
		return errors.New("memory search unavailable")
	}
	return m.manager.SyncWithProgress(ctx, onProgress)
}

type runtimeLogger struct {
	client *AIClient
}

func (l *runtimeLogger) emit(level string, msg string, fields map[string]any) {
	if l == nil || l.client == nil {
		return
	}
	logger := l.client.log.With().Fields(fields).Logger()
	switch level {
	case "debug":
		logger.Debug().Msg(msg)
	case "info":
		logger.Info().Msg(msg)
	case "warn":
		logger.Warn().Msg(msg)
	case "error":
		logger.Error().Msg(msg)
	}
}

func (l *runtimeLogger) Debug(msg string, fields map[string]any) { l.emit("debug", msg, fields) }
func (l *runtimeLogger) Info(msg string, fields map[string]any)  { l.emit("info", msg, fields) }
func (l *runtimeLogger) Warn(msg string, fields map[string]any)  { l.emit("warn", msg, fields) }
func (l *runtimeLogger) Error(msg string, fields map[string]any) { l.emit("error", msg, fields) }
