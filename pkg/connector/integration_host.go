package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/agents"
	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
	integrationmemory "github.com/beeper/ai-bridge/pkg/integrations/memory"
	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
	"github.com/beeper/ai-bridge/pkg/textfs"
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
		result, err := integrationcron.ExecuteTool(ctx, call.Args, integrationcron.ToolExecDeps{
			Status: h.Status,
			List:   h.List,
			Add:    h.Add,
			Update: h.Update,
			Remove: h.Remove,
			Run:    h.Run,
			Runs:   h.Runs,
			Wake:   h.Wake,
			NowMs:  func() int64 { return time.Now().UnixMilli() },
			ResolveCreateContext: func() integrationcron.ToolCreateContext {
				return h.resolveCronToolCreateContext(call.Scope)
			},
			ResolveReminderLines: func(count int) []integrationcron.ReminderContextLine {
				return h.resolveCronReminderLines(call.Scope, count)
			},
			ValidateDeliveryTo: integrationcron.ValidateDeliveryTo,
		})
		return true, result, err
	case ToolNameMemorySearch, ToolNameMemoryGet:
		return integrationmemory.ExecuteTool(ctx, call, integrationmemory.ToolExecDeps{
			GetManager:        h.GetManager,
			ResolveSessionKey: h.resolveMemorySessionKey,
			ResolveCitationsMode: func(scope integrationruntime.ToolScope) string {
				return h.resolveMemoryCitationsMode(scope)
			},
			ShouldIncludeCitations: func(ctx context.Context, scope integrationruntime.ToolScope, mode string) bool {
				return h.shouldIncludeMemoryCitations(ctx, scope, mode)
			},
		})
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

func (oc *AIClient) isMemorySearchExplicitlyDisabled(meta *PortalMetadata) (bool, string) {
	if oc == nil || oc.connector == nil {
		return true, "Missing connector"
	}
	agentID := resolveAgentID(meta)
	cfg, err := resolveMemorySearchConfig(oc, agentID)
	if err != nil {
		return true, err.Error()
	}
	if cfg == nil || !cfg.Enabled {
		return true, "Memory search disabled"
	}
	return false, ""
}

func (oc *AIClient) isCronConfigured() (bool, string) {
	if oc == nil {
		return false, "Cron service not available"
	}
	known, available, _, reason := oc.integratedToolAvailability(&PortalMetadata{}, ToolNameCron)
	if known && available {
		return true, ""
	}
	if strings.TrimSpace(reason) == "" {
		reason = "Cron service not available"
	}
	return false, reason
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
	return integrationmemory.AugmentPrompt(ctx, scope, prompt, integrationmemory.PromptAugmentDeps{
		ShouldInjectContext: h.shouldInjectMemoryPromptContext,
		ShouldBootstrap:     h.shouldBootstrapMemoryPromptContext,
		ResolveBootstrapPaths: func(scope integrationruntime.PromptScope) []string {
			return h.resolveMemoryBootstrapPaths(scope)
		},
		MarkBootstrapped: h.markMemoryPromptBootstrapped,
		ReadSection:      h.readMemoryPromptSection,
	})
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
	return integrationmemory.ExecuteCommand(ctx, call, integrationmemory.CommandExecDeps{
		GetManager:        h.GetManager,
		ResolveSessionKey: h.resolveMemorySessionKey,
		SplitQuotedArgs:   splitQuotedArgs,
		WriteFile:         h.writeMemoryCommandFile,
	})
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
	integrationmemory.HandleOverflow(ctx, call, call.Prompt, integrationmemory.OverflowDeps{
		IsRawMode: func(call any) bool {
			overflowCall, _ := call.(integrationruntime.ContextOverflowCall)
			meta, _ := overflowCall.Meta.(*PortalMetadata)
			return meta != nil && meta.IsRawMode
		},
		ResolveSettings: h.resolveMemoryFlushSettings,
		TrimPrompt: func(prompt []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
			return smartTruncatePrompt(prompt, 0.5)
		},
		ContextWindow: func(call any) int {
			overflowCall, _ := call.(integrationruntime.ContextOverflowCall)
			meta, _ := overflowCall.Meta.(*PortalMetadata)
			return h.client.getModelContextWindow(meta)
		},
		ReserveTokens: func() int {
			compactor := h.client.getCompactor()
			if compactor == nil || compactor.config == nil || compactor.config.ReserveTokens <= 0 {
				return 2000
			}
			return compactor.config.ReserveTokens
		},
		EffectiveModel: func(call any) string {
			overflowCall, _ := call.(integrationruntime.ContextOverflowCall)
			meta, _ := overflowCall.Meta.(*PortalMetadata)
			return h.client.effectiveModel(meta)
		},
		EstimateTokens: estimatePromptTokens,
		AlreadyFlushed: func(call any) bool {
			overflowCall, _ := call.(integrationruntime.ContextOverflowCall)
			meta, _ := overflowCall.Meta.(*PortalMetadata)
			return meta != nil && meta.MemoryFlushAt > 0 && meta.MemoryFlushCompactionCount == meta.CompactionCount
		},
		MarkFlushed: func(ctx context.Context, call any) {
			overflowCall, _ := call.(integrationruntime.ContextOverflowCall)
			portal, _ := overflowCall.Portal.(*bridgev2.Portal)
			meta, _ := overflowCall.Meta.(*PortalMetadata)
			if portal == nil || meta == nil {
				return
			}
			meta.MemoryFlushAt = time.Now().UnixMilli()
			meta.MemoryFlushCompactionCount = meta.CompactionCount
			h.client.savePortalQuiet(ctx, portal, "memory flush")
		},
		RunFlushToolLoop: func(ctx context.Context, call any, model string, prompt []openai.ChatCompletionMessageParamUnion) error {
			overflowCall, _ := call.(integrationruntime.ContextOverflowCall)
			portal, _ := overflowCall.Portal.(*bridgev2.Portal)
			meta, _ := overflowCall.Meta.(*PortalMetadata)
			return h.client.runMemoryFlushToolLoop(ctx, portal, meta, model, prompt)
		},
		OnError: func(ctx context.Context, err error) {
			h.client.loggerForContext(ctx).Warn().Err(err).Msg("memory flush failed")
		},
	})
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
	purgeVectorRowsBestEffort(ctx, login, scope.BridgeID, scope.LoginID)
	purgeAIMemoryTablesBestEffort(ctx, db, scope.BridgeID, scope.LoginID)
	return nil
}

func (h *runtimeIntegrationHost) resolveMemoryFlushSettings() *integrationmemory.FlushSettings {
	if h == nil || h.client == nil {
		return nil
	}
	compactor := h.client.getCompactor()
	if compactor == nil {
		return nil
	}
	config := compactor.config
	if config == nil || config.PruningConfig == nil {
		return &integrationmemory.FlushSettings{
			SoftThresholdTokens: defaultMemoryFlushSoftTokens,
			Prompt:              integrationmemory.EnsureSilentReplyHint(agents.SilentReplyToken, defaultMemoryFlushPrompt),
			SystemPrompt:        integrationmemory.EnsureSilentReplyHint(agents.SilentReplyToken, defaultMemoryFlushSystemPrompt),
		}
	}
	cfg := config.PruningConfig.MemoryFlush
	var enabled *bool
	softThresholdTokens := 0
	prompt := ""
	systemPrompt := ""
	if cfg != nil {
		enabled = cfg.Enabled
		softThresholdTokens = cfg.SoftThresholdTokens
		prompt = cfg.Prompt
		systemPrompt = cfg.SystemPrompt
	}
	return integrationmemory.NormalizeFlushSettings(
		enabled,
		softThresholdTokens,
		prompt,
		systemPrompt,
		defaultMemoryFlushPrompt,
		defaultMemoryFlushSystemPrompt,
		agents.SilentReplyToken,
	)
}

func (h *runtimeIntegrationHost) resolveCronToolCreateContext(scope integrationruntime.ToolScope) integrationcron.ToolCreateContext {
	meta, _ := scope.Meta.(*PortalMetadata)
	portal, _ := scope.Portal.(*bridgev2.Portal)
	return integrationcron.ToolCreateContext{
		AgentID:        resolveAgentID(meta),
		SourceInternal: meta != nil && (meta.IsCronRoom || meta.IsBuilderRoom),
		SourceRoomID: func() string {
			if portal == nil || portal.MXID == "" {
				return ""
			}
			return portal.MXID.String()
		}(),
	}
}

func (h *runtimeIntegrationHost) resolveCronReminderLines(scope integrationruntime.ToolScope, count int) []integrationcron.ReminderContextLine {
	if h == nil || h.client == nil {
		return nil
	}
	portal, _ := scope.Portal.(*bridgev2.Portal)
	if portal == nil || count <= 0 || h.client.UserLogin == nil || h.client.UserLogin.Bridge == nil || h.client.UserLogin.Bridge.DB == nil {
		return nil
	}
	maxMessages := count
	if maxMessages > 10 {
		maxMessages = 10
	}
	history, err := h.client.UserLogin.Bridge.DB.Message.GetLastNInPortal(h.client.backgroundContext(context.Background()), portal.PortalKey, maxMessages)
	if err != nil || len(history) == 0 {
		return nil
	}
	out := make([]integrationcron.ReminderContextLine, 0, len(history))
	for i := len(history) - 1; i >= 0; i-- {
		meta := messageMeta(history[i])
		if meta == nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(meta.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		text := strings.TrimSpace(meta.Body)
		if text == "" {
			continue
		}
		out = append(out, integrationcron.ReminderContextLine{Role: role, Text: text})
	}
	return out
}

func (h *runtimeIntegrationHost) resolveMemorySessionKey(scope integrationruntime.ToolScope) string {
	portal, _ := scope.Portal.(*bridgev2.Portal)
	if portal == nil {
		return ""
	}
	return portal.PortalKey.String()
}

func (h *runtimeIntegrationHost) resolveMemoryCitationsMode(scope integrationruntime.ToolScope) string {
	if h == nil || h.client == nil || h.client.connector == nil || h.client.connector.Config.Memory == nil {
		return "auto"
	}
	mode := strings.ToLower(strings.TrimSpace(h.client.connector.Config.Memory.Citations))
	switch mode {
	case "on", "off", "auto":
		return mode
	default:
		return "auto"
	}
}

func (h *runtimeIntegrationHost) shouldIncludeMemoryCitations(ctx context.Context, scope integrationruntime.ToolScope, mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on":
		return true
	case "off":
		return false
	}
	if h == nil || h.client == nil {
		return true
	}
	portal, _ := scope.Portal.(*bridgev2.Portal)
	if portal == nil {
		return true
	}
	return !h.client.isGroupChat(ctx, portal)
}

func (h *runtimeIntegrationHost) writeMemoryCommandFile(
	ctx context.Context,
	scope integrationruntime.CommandScope,
	mode string,
	path string,
	content string,
	maxBytes int,
) (string, error) {
	if h == nil || h.client == nil || h.client.UserLogin == nil || h.client.UserLogin.Bridge == nil || h.client.UserLogin.Bridge.DB == nil {
		return "", errors.New("memory storage unavailable")
	}
	if len([]byte(content)) > maxBytes {
		return "", fmt.Errorf("content exceeds %d bytes", maxBytes)
	}
	meta, _ := scope.Meta.(*PortalMetadata)
	store := textStoreForScope(h.client, meta)
	if store == nil {
		return "", errors.New("memory storage unavailable")
	}
	if strings.EqualFold(strings.TrimSpace(mode), "append") {
		if existing, found, err := store.Read(ctx, path); err == nil && found {
			sep := "\n"
			if strings.HasSuffix(existing.Content, "\n") || existing.Content == "" {
				sep = ""
			}
			content = existing.Content + sep + content
			if len([]byte(content)) > maxBytes {
				return "", fmt.Errorf("content exceeds %d bytes after append", maxBytes)
			}
		}
	}
	entry, err := store.Write(ctx, path, content)
	if err != nil {
		return "", err
	}
	if entry != nil {
		portal, _ := scope.Portal.(*bridgev2.Portal)
		toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
			Client: h.client,
			Portal: portal,
			Meta:   meta,
		})
		notifyIntegrationFileChanged(toolCtx, entry.Path)
		maybeRefreshAgentIdentity(toolCtx, entry.Path)
		return entry.Path, nil
	}
	return path, nil
}

func textStoreForScope(client *AIClient, meta *PortalMetadata) *textfs.Store {
	if client == nil || client.UserLogin == nil || client.UserLogin.Bridge == nil || client.UserLogin.Bridge.DB == nil {
		return nil
	}
	db := client.bridgeDB()
	if db == nil {
		return nil
	}
	return textfs.NewStore(
		db,
		string(client.UserLogin.Bridge.DB.BridgeID),
		string(client.UserLogin.ID),
		resolveAgentID(meta),
	)
}

func (h *runtimeIntegrationHost) shouldInjectMemoryPromptContext(scope integrationruntime.PromptScope) bool {
	if h == nil || h.client == nil || h.client.connector == nil {
		return false
	}
	meta, _ := scope.Meta.(*PortalMetadata)
	if meta != nil && meta.IsRawMode {
		return false
	}
	return h.client.connector.Config.Memory != nil && h.client.connector.Config.Memory.InjectContext
}

func (h *runtimeIntegrationHost) shouldBootstrapMemoryPromptContext(scope integrationruntime.PromptScope) bool {
	meta, _ := scope.Meta.(*PortalMetadata)
	return meta != nil && meta.MemoryBootstrapAt == 0
}

func (h *runtimeIntegrationHost) resolveMemoryBootstrapPaths(scope integrationruntime.PromptScope) []string {
	if h == nil || h.client == nil {
		return nil
	}
	_, loc := h.client.resolveUserTimezone()
	now := time.Now().In(loc)
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	return []string{
		fmt.Sprintf("memory/%s.md", today),
		fmt.Sprintf("memory/%s.md", yesterday),
	}
}

func (h *runtimeIntegrationHost) markMemoryPromptBootstrapped(ctx context.Context, scope integrationruntime.PromptScope) {
	if h == nil || h.client == nil {
		return
	}
	meta, _ := scope.Meta.(*PortalMetadata)
	portal, _ := scope.Portal.(*bridgev2.Portal)
	if meta == nil || portal == nil {
		return
	}
	meta.MemoryBootstrapAt = time.Now().UnixMilli()
	h.client.savePortalQuiet(ctx, portal, "memory bootstrap")
}

func (h *runtimeIntegrationHost) readMemoryPromptSection(ctx context.Context, scope integrationruntime.PromptScope, path string) string {
	if h == nil || h.client == nil {
		return ""
	}
	meta, _ := scope.Meta.(*PortalMetadata)
	store := textStoreForScope(h.client, meta)
	if store == nil {
		return ""
	}
	entry, found, err := store.Read(ctx, path)
	if err != nil || !found {
		return ""
	}
	content := normalizeNewlines(entry.Content)
	trunc := textfs.TruncateHead(content, textfs.DefaultMaxLines, textfs.DefaultMaxBytes)
	if trunc.FirstLineExceedsLimit {
		return ""
	}
	text := trunc.Content
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if trunc.Truncated {
		text += "\n\n[truncated]"
	}
	return fmt.Sprintf("## %s\n%s", entry.Path, text)
}

func (oc *AIClient) injectMemoryContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
) []openai.ChatCompletionMessageParamUnion {
	host := &runtimeIntegrationHost{client: oc}
	return host.AugmentPrompt(ctx, integrationruntime.PromptScope{
		Client: oc,
		Portal: portal,
		Meta:   meta,
	}, prompt)
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
