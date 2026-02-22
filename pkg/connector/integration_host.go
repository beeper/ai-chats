package connector

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/agents"
	integrationcron "github.com/beeper/ai-bridge/pkg/integrations/cron"
	integrationmemory "github.com/beeper/ai-bridge/pkg/integrations/memory"
	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
	"github.com/beeper/ai-bridge/pkg/textfs"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
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
	return manager, ""
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
		EstimateTokens: estimatePromptTokensForOverflow,
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
			return h.runFlushToolLoop(ctx, portal, meta, model, prompt)
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
		defaultPrompt, defaultSystemPrompt := integrationmemory.DefaultFlushPrompts(agents.SilentReplyToken)
		return &integrationmemory.FlushSettings{
			SoftThresholdTokens: integrationmemory.DefaultFlushSoftTokens,
			Prompt:              integrationmemory.EnsureSilentReplyHint(agents.SilentReplyToken, defaultPrompt),
			SystemPrompt:        integrationmemory.EnsureSilentReplyHint(agents.SilentReplyToken, defaultSystemPrompt),
		}
	}
	cfg := config.PruningConfig.OverflowFlush
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
	defaultPrompt, defaultSystemPrompt := integrationmemory.DefaultFlushPrompts(agents.SilentReplyToken)
	return integrationmemory.NormalizeFlushSettings(
		enabled,
		softThresholdTokens,
		prompt,
		systemPrompt,
		defaultPrompt,
		defaultSystemPrompt,
		agents.SilentReplyToken,
	)
}

func (h *runtimeIntegrationHost) runFlushToolLoop(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	model string,
	messages []openai.ChatCompletionMessageParamUnion,
) error {
	if h == nil || h.client == nil {
		return errors.New("memory flush unavailable")
	}
	tools := h.memoryFlushTools()
	if len(tools) == 0 {
		return nil
	}
	toolParams := ToOpenAIChatTools(tools, &h.client.log)
	toolParams = dedupeChatToolParams(toolParams)
	toolCtx := WithBridgeToolContext(ctx, &BridgeToolContext{
		Client: h.client,
		Portal: portal,
		Meta:   meta,
	})
	return integrationmemory.RunFlushToolLoop(ctx, model, messages, integrationmemory.FlushToolLoopDeps{
		TimeoutMs: int64((2 * time.Minute) / time.Millisecond),
		MaxTurns:  6,
		NextTurn: func(ctx context.Context, model string, messages []openai.ChatCompletionMessageParamUnion) (
			openai.ChatCompletionMessageParamUnion,
			[]integrationmemory.ModelToolCall,
			bool,
			error,
		) {
			req := openai.ChatCompletionNewParams{
				Model:    model,
				Messages: messages,
				Tools:    toolParams,
			}
			resp, err := h.client.api.Chat.Completions.New(ctx, req)
			if err != nil {
				return openai.ChatCompletionMessageParamUnion{}, nil, false, err
			}
			if len(resp.Choices) == 0 {
				return openai.ChatCompletionMessageParamUnion{}, nil, true, nil
			}
			msg := resp.Choices[0].Message
			assistant := msg.ToAssistantMessageParam()
			outAssistant := openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant}
			if len(msg.ToolCalls) == 0 {
				return outAssistant, nil, true, nil
			}
			calls := make([]integrationmemory.ModelToolCall, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				calls = append(calls, integrationmemory.ModelToolCall{
					ID:       call.ID,
					Name:     strings.TrimSpace(call.Function.Name),
					ArgsJSON: call.Function.Arguments,
				})
			}
			return outAssistant, calls, false, nil
		},
		ExecuteTool: func(ctx context.Context, name string, argsJSON string) (string, error) {
			if meta != nil && !h.client.isToolEnabled(meta, name) {
				return "", fmt.Errorf("tool %s is disabled", name)
			}
			return h.client.executeBuiltinTool(toolCtx, portal, name, argsJSON)
		},
		OnToolError: func(name string, err error) {
			h.client.loggerForContext(ctx).Warn().Err(err).Str("tool", name).Msg("memory flush tool failed")
		},
	})
}

func (h *runtimeIntegrationHost) memoryFlushTools() []ToolDefinition {
	var out []ToolDefinition
	for _, tool := range BuiltinTools() {
		if integrationmemory.IsAllowedFlushTool(tool.Name) {
			out = append(out, tool)
		}
	}
	return out
}

func estimatePromptTokensForOverflow(prompt []openai.ChatCompletionMessageParamUnion, model string) int {
	if len(prompt) == 0 {
		return 0
	}
	if count, err := EstimateTokens(prompt, model); err == nil && count > 0 {
		return count
	}
	total := 0
	for _, msg := range prompt {
		total += estimateMessageChars(msg) / charsPerTokenEstimate
	}
	if total <= 0 {
		return len(prompt) * 3
	}
	return total
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

type memoryAgentSearchConfig = agents.MemorySearchConfig

func resolveMemorySearchConfig(client *AIClient, agentID string) (*integrationmemory.ResolvedConfig, error) {
	if client == nil || client.connector == nil {
		return nil, errors.New("missing connector")
	}
	defaults := client.connector.Config.MemorySearch
	var overrides *agents.MemorySearchConfig

	if agentID != "" {
		store := NewAgentStoreAdapter(client)
		agent, err := store.GetAgentByID(client.backgroundContext(context.TODO()), agentID)
		if err == nil && agent != nil {
			overrides = agent.MemorySearch
		}
	}

	resolved := mergeMemorySearchConfig(defaults, overrides)
	if resolved == nil {
		return nil, errors.New("memory search disabled")
	}
	return resolved, nil
}

func mergeMemorySearchConfig(
	defaults *MemorySearchConfig,
	overrides *agents.MemorySearchConfig,
) *integrationmemory.ResolvedConfig {
	return integrationmemory.MergeSearchConfig(convertMemorySearchDefaults(defaults), overrides)
}

func convertMemorySearchDefaults(defaults *MemorySearchConfig) *agents.MemorySearchConfig {
	if defaults == nil {
		return nil
	}
	raw, err := json.Marshal(defaults)
	if err != nil {
		return nil
	}
	var out agents.MemorySearchConfig
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return &out
}

func resolveOpenAIEmbeddingConfig(client *AIClient, cfg *integrationmemory.ResolvedConfig) (string, string, map[string]string) {
	var apiKey string
	var baseURL string
	if strings.TrimSpace(cfg.Remote.APIKey) != "" {
		apiKey = strings.TrimSpace(cfg.Remote.APIKey)
	} else if client != nil && client.connector != nil {
		meta := loginMetadata(client.UserLogin)
		apiKey = strings.TrimSpace(client.connector.resolveOpenAIAPIKey(meta))
		if meta != nil {
			if apiKey == "" && meta.Provider == ProviderMagicProxy {
				apiKey = strings.TrimSpace(meta.APIKey)
			}
			if apiKey == "" && meta.Provider == ProviderBeeper {
				services := client.connector.resolveServiceConfig(meta)
				if svc, ok := services[serviceOpenRouter]; ok {
					apiKey = strings.TrimSpace(svc.APIKey)
					if baseURL == "" {
						baseURL = strings.TrimSpace(svc.BaseURL)
					}
				}
			}
		}
	}
	if strings.TrimSpace(cfg.Remote.BaseURL) != "" {
		baseURL = strings.TrimSpace(cfg.Remote.BaseURL)
	}
	if baseURL == "" && client != nil && client.connector != nil {
		if meta := loginMetadata(client.UserLogin); meta != nil {
			if meta.Provider == ProviderMagicProxy {
				base := normalizeMagicProxyBaseURL(meta.BaseURL)
				if base != "" {
					baseURL = joinProxyPath(base, "/openrouter/v1")
				}
			} else if meta.Provider == ProviderBeeper {
				services := client.connector.resolveServiceConfig(meta)
				if svc, ok := services[serviceOpenRouter]; ok && strings.TrimSpace(svc.BaseURL) != "" {
					baseURL = strings.TrimSpace(svc.BaseURL)
				}
			}
		}
		if baseURL == "" {
			baseURL = client.connector.resolveOpenAIBaseURL()
		}
	}
	return apiKey, baseURL, cfg.Remote.Headers
}

// resolveDirectOpenAIEmbeddingConfig resolves the direct OpenAI endpoint
// (/openai/v1) for batch API calls that require OpenAI-specific endpoints
// like /files and /batches which OpenRouter does not support.
func resolveDirectOpenAIEmbeddingConfig(client *AIClient, cfg *integrationmemory.ResolvedConfig) (string, string, map[string]string) {
	var apiKey string
	var baseURL string
	if strings.TrimSpace(cfg.Remote.APIKey) != "" {
		apiKey = strings.TrimSpace(cfg.Remote.APIKey)
	} else if client != nil && client.connector != nil {
		meta := loginMetadata(client.UserLogin)
		apiKey = strings.TrimSpace(client.connector.resolveOpenAIAPIKey(meta))
		if meta != nil {
			if apiKey == "" && meta.Provider == ProviderMagicProxy {
				apiKey = strings.TrimSpace(meta.APIKey)
			}
			if apiKey == "" && meta.Provider == ProviderBeeper {
				services := client.connector.resolveServiceConfig(meta)
				if svc, ok := services[serviceOpenAI]; ok {
					apiKey = strings.TrimSpace(svc.APIKey)
					if baseURL == "" {
						baseURL = strings.TrimSpace(svc.BaseURL)
					}
				}
			}
		}
	}
	if strings.TrimSpace(cfg.Remote.BaseURL) != "" {
		baseURL = strings.TrimSpace(cfg.Remote.BaseURL)
	}
	if baseURL == "" && client != nil && client.connector != nil {
		if meta := loginMetadata(client.UserLogin); meta != nil {
			if meta.Provider == ProviderMagicProxy {
				base := normalizeMagicProxyBaseURL(meta.BaseURL)
				if base != "" {
					baseURL = joinProxyPath(base, "/openai/v1")
				}
			} else if meta.Provider == ProviderBeeper {
				services := client.connector.resolveServiceConfig(meta)
				if svc, ok := services[serviceOpenAI]; ok && strings.TrimSpace(svc.BaseURL) != "" {
					baseURL = strings.TrimSpace(svc.BaseURL)
				}
			}
		}
		if baseURL == "" {
			baseURL = client.connector.resolveOpenAIBaseURL()
		}
	}
	return apiKey, baseURL, cfg.Remote.Headers
}

func resolveGeminiEmbeddingConfig(_ *AIClient, cfg *integrationmemory.ResolvedConfig) (string, string, map[string]string) {
	apiKey := strings.TrimSpace(cfg.Remote.APIKey)
	baseURL := strings.TrimSpace(cfg.Remote.BaseURL)
	if baseURL == "" {
		baseURL = integrationmemory.DefaultGeminiBaseURL
	}
	return apiKey, baseURL, cfg.Remote.Headers
}

const memorySearchTimeout = 10 * time.Second

type memoryRuntimeAdapter struct {
	client *AIClient
}

func (a *memoryRuntimeAdapter) ResolveConfig(agentID string) (*integrationmemory.ResolvedConfig, error) {
	if a == nil || a.client == nil {
		return nil, nil
	}
	return resolveMemorySearchConfig(a.client, agentID)
}

func (a *memoryRuntimeAdapter) ResolveOpenAIEmbeddingConfig(cfg *integrationmemory.ResolvedConfig) (string, string, map[string]string) {
	if a == nil {
		return "", "", nil
	}
	return resolveOpenAIEmbeddingConfig(a.client, cfg)
}

func (a *memoryRuntimeAdapter) ResolveDirectOpenAIEmbeddingConfig(cfg *integrationmemory.ResolvedConfig) (string, string, map[string]string) {
	if a == nil {
		return "", "", nil
	}
	return resolveDirectOpenAIEmbeddingConfig(a.client, cfg)
}

func (a *memoryRuntimeAdapter) ResolveGeminiEmbeddingConfig(cfg *integrationmemory.ResolvedConfig) (string, string, map[string]string) {
	if a == nil {
		return "", "", nil
	}
	return resolveGeminiEmbeddingConfig(a.client, cfg)
}

func (a *memoryRuntimeAdapter) ResolvePromptWorkspaceDir() string {
	return resolvePromptWorkspaceDir()
}

func (a *memoryRuntimeAdapter) ListSessionPortals(ctx context.Context, loginID, agentID string) ([]integrationmemory.SessionPortal, error) {
	if a == nil || a.client == nil || a.client.UserLogin == nil || a.client.UserLogin.Bridge == nil || a.client.UserLogin.Bridge.DB == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(loginID) == "" {
		loginID = string(a.client.UserLogin.ID)
	}

	allowedShared := map[string]struct{}{}
	if ups, err := a.client.UserLogin.Bridge.DB.UserPortal.GetAllForLogin(ctx, a.client.UserLogin.UserLogin); err == nil {
		for _, up := range ups {
			if up == nil || up.Portal.Receiver != "" {
				continue
			}
			allowedShared[up.Portal.String()] = struct{}{}
		}
	}

	portals, err := a.client.UserLogin.Bridge.DB.Portal.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]integrationmemory.SessionPortal, 0, len(portals))
	for _, portal := range portals {
		if portal == nil || portal.MXID == "" {
			continue
		}
		if portal.Receiver != "" && string(portal.Receiver) != loginID {
			continue
		}
		if portal.Receiver == "" && len(allowedShared) > 0 {
			if _, ok := allowedShared[portal.PortalKey.String()]; !ok {
				continue
			}
		}
		meta, ok := portal.Metadata.(*PortalMetadata)
		if !ok || meta == nil || meta.IsCronRoom {
			continue
		}
		if resolveAgentID(meta) != agentID {
			continue
		}
		key := portal.PortalKey.String()
		if key == "" {
			continue
		}
		out = append(out, integrationmemory.SessionPortal{Key: key, PortalKey: portal.PortalKey})
	}
	return out, nil
}

func (a *memoryRuntimeAdapter) BridgeDB() *dbutil.Database {
	if a == nil || a.client == nil {
		return nil
	}
	return a.client.bridgeDB()
}

func (a *memoryRuntimeAdapter) BridgeID() string {
	if a == nil || a.client == nil || a.client.UserLogin == nil || a.client.UserLogin.Bridge == nil || a.client.UserLogin.Bridge.DB == nil {
		return ""
	}
	return string(a.client.UserLogin.Bridge.DB.BridgeID)
}

func (a *memoryRuntimeAdapter) LoginID() string {
	if a == nil || a.client == nil || a.client.UserLogin == nil {
		return ""
	}
	return string(a.client.UserLogin.ID)
}

func (a *memoryRuntimeAdapter) Logger() zerolog.Logger {
	if a == nil || a.client == nil {
		return zerolog.Logger{}
	}
	return a.client.log
}

func (oc *AIClient) getMemoryManager(agentID string) (*integrationmemory.MemorySearchManager, string) {
	if oc == nil {
		return nil, "memory search unavailable"
	}
	manager, errMsg := integrationmemory.GetMemorySearchManager(&memoryRuntimeAdapter{client: oc}, agentID)
	if manager == nil {
		if errMsg == "" {
			errMsg = "memory search unavailable"
		}
		return nil, errMsg
	}
	return manager, ""
}

func loadMemoryChunkIDsByAgentBestEffort(ctx context.Context, db *dbutil.Database, bridgeID, loginID string) map[string][]string {
	return integrationmemory.LoadChunkIDsByAgentBestEffort(ctx, db, bridgeID, loginID)
}

func purgeAIMemoryTablesBestEffort(ctx context.Context, db *dbutil.Database, bridgeID, loginID string) {
	integrationmemory.PurgeTablesBestEffort(ctx, db, bridgeID, loginID)
}

func purgeVectorRowsBestEffort(ctx context.Context, login *bridgev2.UserLogin, bridgeID, loginID string) {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return
	}
	db := bridgeDBFromLogin(login)
	if db == nil {
		return
	}
	client, ok := login.Client.(*AIClient)
	if !ok || client == nil {
		return
	}
	cfg, err := resolveMemorySearchConfig(client, "")
	if err != nil || cfg == nil || !cfg.Store.Vector.Enabled {
		return
	}
	extPath := strings.TrimSpace(cfg.Store.Vector.ExtensionPath)
	if extPath == "" {
		return
	}
	integrationmemory.PurgeVectorRowsBestEffort(ctx, db, bridgeID, loginID, extPath)
}

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

func (oc *AIClient) resolveCronDeliveryTarget(agentID string, delivery *integrationcron.Delivery) deliveryTarget {
	resolved := integrationcron.ResolveCronDeliveryTarget(agentID, delivery, integrationcron.DeliveryResolverDeps{
		ResolveLastTarget: func(agentID string) (string, string, bool) {
			storeRef, mainKey := oc.resolveHeartbeatMainSessionRef(agentID)
			entry, ok := oc.getSessionEntry(context.Background(), storeRef, mainKey)
			if !ok {
				return "", "", false
			}
			return entry.LastChannel, entry.LastTo, true
		},
		IsStaleTarget: func(roomID, agentID string) bool {
			candidate := strings.TrimSpace(roomID)
			if candidate == "" || !strings.HasPrefix(candidate, "!") {
				return false
			}
			if p := oc.portalByRoomID(context.Background(), id.RoomID(candidate)); p != nil {
				if meta := portalMeta(p); meta != nil && normalizeAgentID(meta.AgentID) != normalizeAgentID(agentID) {
					return true
				}
			}
			return false
		},
		LastActiveRoomID: func(agentID string) string {
			if portal := oc.lastActivePortal(agentID); portal != nil && portal.MXID != "" {
				return portal.MXID.String()
			}
			return ""
		},
		DefaultChatRoomID: func() string {
			if portal := oc.defaultChatPortal(); portal != nil && portal.MXID != "" {
				return portal.MXID.String()
			}
			return ""
		},
		ResolvePortalByRoom: func(roomID string) any {
			return oc.portalByRoomID(context.Background(), id.RoomID(roomID))
		},
		IsLoggedIn: oc.IsLoggedIn,
	})
	out := deliveryTarget{Channel: resolved.Channel, Reason: resolved.Reason}
	if portal, ok := resolved.Portal.(*bridgev2.Portal); ok && portal != nil {
		out.Portal = portal
		out.RoomID = portal.MXID
	}
	return out
}

func cronPortalKey(loginID networkid.UserLoginID, agentID, jobID string) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("openai:%s:cron:%s:%s", loginID, url.PathEscape(agentID), url.PathEscape(jobID))),
		Receiver: loginID,
	}
}

func (oc *AIClient) getOrCreateCronRoom(ctx context.Context, agentID, jobID, jobName string) (*bridgev2.Portal, error) {
	if oc == nil || oc.UserLogin == nil {
		return nil, errors.New("missing login")
	}
	room, err := integrationcron.GetOrCreateCronRoom(ctx, agentID, jobID, jobName, integrationcron.RoomResolverDeps{
		DefaultAgentID: agents.DefaultAgentID,
		ResolveRoom: func(ctx context.Context, normalizedAgentID, normalizedJobID string) (any, string, error) {
			portalKey := cronPortalKey(oc.UserLogin.ID, normalizedAgentID, normalizedJobID)
			portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
			if err != nil {
				return nil, "", err
			}
			return portal, portal.MXID.String(), nil
		},
		CreateRoom: func(ctx context.Context, normalizedAgentID, normalizedJobID, displayName string) (any, error) {
			portalKey := cronPortalKey(oc.UserLogin.ID, normalizedAgentID, normalizedJobID)
			portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
			if err != nil {
				return nil, fmt.Errorf("failed to get portal: %w", err)
			}
			portal.Metadata = &PortalMetadata{
				IsCronRoom: true,
				CronJobID:  normalizedJobID,
				AgentID:    normalizedAgentID,
			}
			portal.Name = displayName
			portal.NameSet = true
			if err := portal.Save(ctx); err != nil {
				return nil, fmt.Errorf("failed to save portal: %w", err)
			}
			chatInfo := &bridgev2.ChatInfo{Name: &portal.Name}
			if err := portal.CreateMatrixRoom(ctx, oc.UserLogin, chatInfo); err != nil {
				return nil, fmt.Errorf("failed to create Matrix room: %w", err)
			}
			return portal, nil
		},
		LogCreated: func(ctx context.Context, agentID, jobID string, room any) {
			portal, _ := room.(*bridgev2.Portal)
			if portal == nil {
				return
			}
			oc.loggerForContext(ctx).Info().Str("agent_id", strings.TrimSpace(agentID)).Str("job_id", strings.TrimSpace(jobID)).Stringer("portal", portal.PortalKey).Msg("Created cron room")
		},
	})
	if err != nil {
		return nil, err
	}
	portal, _ := room.(*bridgev2.Portal)
	if portal == nil {
		return nil, errors.New("failed to resolve cron room")
	}
	return portal, nil
}

const (
	cronDeliveryTimeout = 10 * time.Second
)

func (oc *AIClient) runCronIsolatedAgentJob(ctx context.Context, job integrationcron.Job, message string) (status string, summary string, outputText string, err error) {
	if oc == nil || oc.UserLogin == nil {
		return "error", "", "", errors.New("missing client")
	}
	return integrationcron.RunCronIsolatedAgentJob(ctx, job, message, integrationcron.IsolatedRunnerDeps{
		DeliveryTimeout: cronDeliveryTimeout,
		MergeContext:    oc.mergeCronContext,
		ResolveAgentID: func(raw string) string {
			return resolveCronAgentID(raw, &oc.connector.Config)
		},
		GetOrCreateRoom: func(ctx context.Context, agentID, jobID, jobName string) (any, error) {
			return oc.getOrCreateCronRoom(ctx, agentID, jobID, jobName)
		},
		BuildDispatchMetadata: func(room any, patch integrationcron.MetadataPatch) any {
			portal, _ := room.(*bridgev2.Portal)
			return oc.buildCronDispatchMetadata(portal, patch)
		},
		NormalizeThinkingLevel: normalizeThinkingLevel,
		SessionKey:             cronSessionKey,
		UpdateSessionEntry: func(ctx context.Context, sessionKey string, updater func(entry integrationcron.SessionEntry) integrationcron.SessionEntry) {
			oc.updateCronSessionEntry(ctx, sessionKey, updater)
		},
		ResolveUserTimezone: func() string {
			tz, _ := oc.resolveUserTimezone()
			return tz
		},
		LastAssistantMessage: func(ctx context.Context, room any) (string, int64) {
			portal, _ := room.(*bridgev2.Portal)
			return oc.lastAssistantMessageInfo(ctx, portal)
		},
		DispatchInternalMessage: func(ctx context.Context, room any, metadata any, message string) error {
			portal, _ := room.(*bridgev2.Portal)
			if portal == nil {
				return errors.New("missing portal")
			}
			metaSnapshot, _ := metadata.(*PortalMetadata)
			if metaSnapshot == nil {
				metaSnapshot = &PortalMetadata{}
			}
			_, _, dispatchErr := oc.dispatchInternalMessage(ctx, portal, metaSnapshot, message, "cron", false)
			return dispatchErr
		},
		WaitForAssistantMessage: func(ctx context.Context, room any, lastID string, lastTimestamp int64) (integrationcron.AssistantMessage, bool) {
			portal, _ := room.(*bridgev2.Portal)
			msg, found := oc.waitForNewAssistantMessage(ctx, portal, lastID, lastTimestamp)
			if !found || msg == nil {
				return integrationcron.AssistantMessage{}, false
			}
			body := ""
			model := ""
			var promptTokens, completionTokens int64
			if meta := messageMeta(msg); meta != nil {
				body = strings.TrimSpace(meta.Body)
				model = strings.TrimSpace(meta.Model)
				promptTokens = meta.PromptTokens
				completionTokens = meta.CompletionTokens
			}
			return integrationcron.AssistantMessage{
				Body:             body,
				Model:            model,
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
			}, true
		},
		ResolveAckMaxChars: func(agentID string) int {
			return resolveHeartbeatAckMaxChars(&oc.connector.Config, resolveHeartbeatConfig(&oc.connector.Config, agentID))
		},
		ResolveDeliveryTarget: func(agentID string, delivery *integrationcron.Delivery) integrationcron.DeliveryTarget {
			target := oc.resolveCronDeliveryTarget(agentID, delivery)
			return integrationcron.DeliveryTarget{
				Portal:  target.Portal,
				RoomID:  target.RoomID.String(),
				Channel: target.Channel,
				Reason:  target.Reason,
			}
		},
		SendDeliveryMessage: func(ctx context.Context, portal any, body string) error {
			targetPortal, _ := portal.(*bridgev2.Portal)
			if targetPortal == nil {
				return errors.New("missing delivery portal")
			}
			return oc.sendPlainAssistantMessageWithResult(ctx, targetPortal, body)
		},
	})
}

func (oc *AIClient) buildCronDispatchMetadata(portal *bridgev2.Portal, patch integrationcron.MetadataPatch) *PortalMetadata {
	meta := portalMeta(portal)
	metaSnapshot := clonePortalMetadata(meta)
	if metaSnapshot == nil {
		metaSnapshot = &PortalMetadata{}
	}
	metaSnapshot.AgentID = patch.AgentID
	if patch.Model != nil {
		metaSnapshot.Model = strings.TrimSpace(*patch.Model)
	}
	if patch.ReasoningEffort != nil {
		metaSnapshot.ReasoningEffort = strings.TrimSpace(*patch.ReasoningEffort)
	}
	if patch.DisableMessageTool {
		metaSnapshot.DisabledTools = []string{ToolNameMessage}
	}
	return metaSnapshot
}

// mergeCronContext ensures cron runs are cancelled on disconnect while preserving deadlines.
func (oc *AIClient) mergeCronContext(ctx context.Context) (context.Context, context.CancelFunc) {
	var base context.Context
	if oc != nil && oc.disconnectCtx != nil {
		base = oc.disconnectCtx
	} else if oc != nil && oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.BackgroundCtx != nil {
		base = oc.UserLogin.Bridge.BackgroundCtx
	} else {
		base = context.Background()
	}

	if model, ok := modelOverrideFromContext(ctx); ok {
		base = withModelOverride(base, model)
	}

	var merged context.Context
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); ok {
		merged, cancel = context.WithDeadline(base, deadline)
	} else {
		merged, cancel = context.WithCancel(base)
	}
	return oc.loggerForContext(ctx).WithContext(merged), cancel
}

func (oc *AIClient) lastAssistantMessageInfo(ctx context.Context, portal *bridgev2.Portal) (string, int64) {
	if portal == nil {
		return "", 0
	}
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 20)
	if err != nil {
		return "", 0
	}
	bestID := ""
	bestTS := int64(0)
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		meta := messageMeta(msg)
		if meta == nil || meta.Role != "assistant" {
			continue
		}
		ts := msg.Timestamp.UnixMilli()
		if bestID == "" || ts > bestTS {
			bestID = msg.MXID.String()
			bestTS = ts
		}
	}
	return bestID, bestTS
}

func (oc *AIClient) waitForNewAssistantMessage(ctx context.Context, portal *bridgev2.Portal, lastID string, lastTimestamp int64) (*database.Message, bool) {
	if portal == nil {
		return nil, false
	}
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 20)
	if err != nil {
		return nil, false
	}
	var candidate *database.Message
	candidateTS := lastTimestamp
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		meta := messageMeta(msg)
		if meta == nil || meta.Role != "assistant" {
			continue
		}
		idStr := msg.MXID.String()
		ts := msg.Timestamp.UnixMilli()
		if ts < lastTimestamp {
			continue
		}
		if ts == lastTimestamp && idStr == lastID {
			continue
		}
		if candidate == nil || ts > candidateTS {
			candidate = msg
			candidateTS = ts
		}
	}
	if candidate == nil {
		return nil, false
	}
	return candidate, true
}
