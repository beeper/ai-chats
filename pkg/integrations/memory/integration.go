package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/ai-bridge/pkg/agents"
	iruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
	memorycore "github.com/beeper/ai-bridge/pkg/memory"
	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

const moduleName = "memory"

type SearchOptions = memorycore.SearchOptions
type SearchResult = memorycore.SearchResult
type FallbackStatus = memorycore.FallbackStatus
type ProviderStatus = memorycore.ProviderStatus
type ResolvedConfig = memorycore.ResolvedConfig

type StatusDetails = MemorySearchStatus

type Manager interface {
	Status() ProviderStatus
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
	ReadFile(ctx context.Context, relPath string, from, lines *int) (map[string]any, error)
	StatusDetails(ctx context.Context) (*StatusDetails, error)
	SyncWithProgress(ctx context.Context, onProgress func(completed, total int, label string)) error
}

// Integration is the self-owned memory integration module.
// It implements ToolIntegration, PromptIntegration, CommandIntegration,
// EventIntegration, LoginPurgeIntegration, and LoginLifecycleIntegration
// directly, wiring all deps from Host
// capability interfaces.
type Integration struct {
	host iruntime.Host
}

func New(host iruntime.Host) iruntime.ModuleHooks {
	if host == nil {
		return nil
	}
	return &Integration{host: host}
}

func (i *Integration) Name() string { return moduleName }

// ---- ToolIntegration ----

func (i *Integration) ToolDefinitions(_ context.Context, _ iruntime.ToolScope) []iruntime.ToolDefinition {
	return []iruntime.ToolDefinition{
		{
			Name:        toolspec.MemorySearchName,
			Description: toolspec.MemorySearchDescription,
			Parameters:  toolspec.MemorySearchSchema(),
		},
		{
			Name:        toolspec.MemoryGetName,
			Description: toolspec.MemoryGetDescription,
			Parameters:  toolspec.MemoryGetSchema(),
		},
	}
}

func (i *Integration) ExecuteTool(ctx context.Context, call iruntime.ToolCall) (bool, string, error) {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if name != "memory_search" && name != "memory_get" {
		return false, "", nil
	}
	return ExecuteTool(ctx, call, i.buildToolExecDeps())
}

func (i *Integration) ToolAvailability(_ context.Context, scope iruntime.ToolScope, toolName string) (bool, bool, iruntime.SettingSource, string) {
	name := strings.ToLower(strings.TrimSpace(toolName))
	if name != "memory_search" && name != "memory_get" {
		return false, false, iruntime.SourceGlobalDefault, ""
	}
	// Check if memory search is explicitly disabled for this agent.
	ma, _ := i.host.(iruntime.MetadataAccess)
	if ma != nil && scope.Meta != nil {
		agentID := ma.AgentIDFromMeta(scope.Meta)
		_, errMsg := i.getManager(agentID)
		if errMsg != "" {
			return true, false, iruntime.SourceProviderLimit, errMsg
		}
	}
	return true, true, iruntime.SourceGlobalDefault, ""
}

// ---- PromptIntegration ----

func (i *Integration) AdditionalSystemMessages(_ context.Context, _ iruntime.PromptScope) []openai.ChatCompletionMessageParamUnion {
	return nil
}

func (i *Integration) AugmentPrompt(ctx context.Context, scope iruntime.PromptScope, prompt []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
	return AugmentPrompt(ctx, scope, prompt, PromptAugmentDeps{
		ShouldInjectContext: i.shouldInjectMemoryPromptContext,
		ShouldBootstrap:     i.shouldBootstrapMemoryPromptContext,
		ResolveBootstrapPaths: func(scope iruntime.PromptScope) []string {
			return i.resolveMemoryBootstrapPaths(scope)
		},
		MarkBootstrapped: i.markMemoryPromptBootstrapped,
		ReadSection:      i.readMemoryPromptSection,
	})
}

// ---- CommandIntegration ----

func (i *Integration) CommandDefinitions(_ context.Context, _ iruntime.CommandScope) []iruntime.CommandDefinition {
	return []iruntime.CommandDefinition{{
		Name:           "memory",
		Description:    "Inspect and edit memory files/index",
		Args:           "<status|reindex|search|get|set|append> [...]",
		RequiresPortal: true,
		RequiresLogin:  true,
		AdminOnly:      true,
	}}
}

func (i *Integration) ExecuteCommand(ctx context.Context, call iruntime.CommandCall) (bool, error) {
	if strings.ToLower(strings.TrimSpace(call.Name)) != moduleName {
		return false, nil
	}
	return ExecuteCommand(ctx, call, i.buildCommandExecDeps())
}

// ---- EventIntegration ----

func (i *Integration) OnSessionMutation(ctx context.Context, evt iruntime.SessionMutationEvent) {
	agentID := i.agentIDFromEventMeta(evt.Meta)
	manager, _ := i.getManager(agentID)
	if manager == nil {
		return
	}
	if msm, ok := manager.(*MemorySearchManager); ok {
		msm.NotifySessionChanged(ctx, evt.SessionKey, evt.Force)
	}
}

func (i *Integration) OnFileChanged(_ context.Context, evt iruntime.FileChangedEvent) {
	agentID := i.agentIDFromEventMeta(evt.Meta)
	manager, _ := i.getManager(agentID)
	if manager == nil {
		return
	}
	if msm, ok := manager.(*MemorySearchManager); ok {
		msm.NotifyFileChanged(evt.Path)
	}
}

func (i *Integration) OnContextOverflow(ctx context.Context, call iruntime.ContextOverflowCall) {
	HandleOverflow(ctx, call, call.Prompt, i.buildOverflowDeps())
}

func (i *Integration) OnCompactionLifecycle(ctx context.Context, evt iruntime.CompactionLifecycleEvent) {
	ma, ok := i.host.(iruntime.MetadataAccess)
	if !ok || evt.Meta == nil {
		return
	}
	switch evt.Phase {
	case iruntime.CompactionLifecycleStart:
		ma.SetModuleMeta(evt.Meta, "compaction_in_flight", true)
	case iruntime.CompactionLifecycleEnd:
		ma.SetModuleMeta(evt.Meta, "compaction_in_flight", false)
		ma.SetModuleMeta(evt.Meta, "last_compaction_at", time.Now().UnixMilli())
		ma.SetModuleMeta(evt.Meta, "last_compaction_dropped_count", evt.DroppedCount)
	case iruntime.CompactionLifecycleFail:
		ma.SetModuleMeta(evt.Meta, "compaction_in_flight", false)
		ma.SetModuleMeta(evt.Meta, "last_compaction_error", strings.TrimSpace(evt.Error))
	case iruntime.CompactionLifecycleRefresh:
		ma.SetModuleMeta(evt.Meta, "last_compaction_refresh_at", time.Now().UnixMilli())
	}
	pm, ok := i.host.(iruntime.PortalManager)
	if !ok || evt.Portal == nil {
		return
	}
	if err := pm.SavePortal(ctx, evt.Portal, "compaction lifecycle"); err != nil {
		i.host.Logger().Warn("failed to persist compaction lifecycle metadata", map[string]any{
			"error": err.Error(),
			"phase": string(evt.Phase),
		})
	}
}

// ---- LoginLifecycleIntegration ----

func (i *Integration) StopForLogin(bridgeID, loginID string) {
	StopManagersForLogin(bridgeID, loginID)
}

// ---- LoginPurgeIntegration ----

func (i *Integration) PurgeForLogin(ctx context.Context, scope iruntime.LoginScope) error {
	db := i.resolveBridgeDB()
	if db == nil {
		return nil
	}
	StopManagersForLogin(scope.BridgeID, scope.LoginID)
	// Resolve vector extension path from config for vector row purge.
	cfg := i.resolveMemorySearchConfig("")
	if cfg != nil && cfg.Store.Vector.Enabled {
		extPath := strings.TrimSpace(cfg.Store.Vector.ExtensionPath)
		if extPath != "" {
			PurgeVectorRowsBestEffort(ctx, db, scope.BridgeID, scope.LoginID, extPath)
		}
	}
	PurgeTablesBestEffort(ctx, db, scope.BridgeID, scope.LoginID)
	return nil
}

// ---- private: tool deps wiring ----

func (i *Integration) managerForScope(scope iruntime.ToolScope) (Manager, string) {
	agentID := i.agentIDFromEventMeta(scope.Meta)
	return i.getManager(agentID)
}

func (i *Integration) sessionKeyForScope(scope iruntime.ToolScope) string {
	pm, ok := i.host.(iruntime.PortalManager)
	if !ok || scope.Portal == nil {
		return ""
	}
	return pm.PortalKeyString(scope.Portal)
}

func (i *Integration) buildToolExecDeps() ToolExecDeps {
	return ToolExecDeps{
		GetManager:        i.managerForScope,
		ResolveSessionKey: i.sessionKeyForScope,
		ResolveCitationsMode: func(_ iruntime.ToolScope) string {
			return i.resolveMemoryCitationsMode()
		},
		ShouldIncludeCitations: func(ctx context.Context, scope iruntime.ToolScope, mode string) bool {
			return i.shouldIncludeMemoryCitations(ctx, scope, mode)
		},
	}
}

func (i *Integration) buildCommandExecDeps() CommandExecDeps {
	return CommandExecDeps{
		GetManager:        i.managerForScope,
		ResolveSessionKey: i.sessionKeyForScope,
		SplitQuotedArgs:   splitQuotedArgs,
		WriteFile: func(ctx context.Context, scope iruntime.CommandScope, mode string, path string, content string, maxBytes int) (string, error) {
			return i.writeMemoryCommandFile(ctx, scope, mode, path, content, maxBytes)
		},
	}
}

// asOverflowCall safely extracts an overflow call from the generic call argument.
func asOverflowCall(call any) (iruntime.ContextOverflowCall, bool) {
	oc, ok := call.(iruntime.ContextOverflowCall)
	return oc, ok
}

// toInt64 extracts an int64 from a value that may be int, int64, or float64.
func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	default:
		return 0
	}
}

func (i *Integration) buildOverflowDeps() OverflowDeps {
	return OverflowDeps{
		IsSimpleMode: func(call any) bool {
			ma, ok := i.host.(iruntime.MetadataAccess)
			if !ok {
				return false
			}
			oc, ok := asOverflowCall(call)
			if !ok {
				return false
			}
			return ma.IsSimpleMode(oc.Meta)
		},
		ResolveSettings: i.resolveOverflowFlushSettings,
		TrimPrompt: func(prompt []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
			oh, ok := i.host.(iruntime.OverflowHelper)
			if !ok {
				return prompt
			}
			return oh.SmartTruncatePrompt(prompt, 0.5)
		},
		ContextWindow: func(call any) int {
			mh, ok := i.host.(iruntime.ModelHelper)
			if !ok {
				return 128000
			}
			oc, ok := asOverflowCall(call)
			if !ok {
				return 128000
			}
			return mh.ContextWindow(oc.Meta)
		},
		ReserveTokens: func() int {
			oh, ok := i.host.(iruntime.OverflowHelper)
			if !ok {
				return 2000
			}
			return oh.CompactorReserveTokens()
		},
		EffectiveModel: func(call any) string {
			mh, ok := i.host.(iruntime.ModelHelper)
			if !ok {
				return ""
			}
			oc, ok := asOverflowCall(call)
			if !ok {
				return ""
			}
			return mh.EffectiveModel(oc.Meta)
		},
		EstimateTokens: func(prompt []openai.ChatCompletionMessageParamUnion, model string) int {
			oh, ok := i.host.(iruntime.OverflowHelper)
			if !ok {
				return 0
			}
			return oh.EstimateTokens(prompt, model)
		},
		AlreadyFlushed: func(call any) bool {
			ma, ok := i.host.(iruntime.MetadataAccess)
			if !ok {
				return false
			}
			oc, ok := asOverflowCall(call)
			if !ok {
				return false
			}
			flushAtMs := toInt64(ma.GetModuleMeta(oc.Meta, "overflow_flush_at"))
			if flushAtMs == 0 {
				return false
			}
			flushCC := toInt64(ma.GetModuleMeta(oc.Meta, "overflow_flush_compaction_count"))
			return int(flushCC) == ma.CompactionCount(oc.Meta)
		},
		MarkFlushed: func(ctx context.Context, call any) {
			oc, _ := asOverflowCall(call)
			ma, ok := i.host.(iruntime.MetadataAccess)
			if !ok || oc.Portal == nil || oc.Meta == nil {
				return
			}
			ma.SetModuleMeta(oc.Meta, "overflow_flush_at", time.Now().UnixMilli())
			ma.SetModuleMeta(oc.Meta, "overflow_flush_compaction_count", ma.CompactionCount(oc.Meta))
			pm, ok := i.host.(iruntime.PortalManager)
			if !ok {
				return
			}
			_ = pm.SavePortal(ctx, oc.Portal, "overflow flush")
		},
		RunFlushToolLoop: func(ctx context.Context, call any, model string, prompt []openai.ChatCompletionMessageParamUnion) (bool, error) {
			oc, _ := asOverflowCall(call)
			return i.runFlushToolLoop(ctx, oc.Portal, oc.Meta, model, prompt)
		},
		OnError: func(_ context.Context, err error) {
			i.host.Logger().Warn("overflow flush failed", map[string]any{"error": err.Error()})
		},
	}
}

// ---- private: prompt context ----

func (i *Integration) shouldInjectMemoryPromptContext(scope iruntime.PromptScope) bool {
	ma, ok := i.host.(iruntime.MetadataAccess)
	if !ok {
		return false
	}
	if scope.Meta != nil && ma.IsSimpleMode(scope.Meta) {
		return false
	}
	cl := i.host.ConfigLookup()
	if cl == nil {
		return false
	}
	cfg := cl.ModuleConfig(moduleName)
	if cfg == nil {
		return false
	}
	inject, _ := cfg["inject_context"].(bool)
	return inject
}

func (i *Integration) shouldBootstrapMemoryPromptContext(scope iruntime.PromptScope) bool {
	ma, ok := i.host.(iruntime.MetadataAccess)
	if !ok {
		return false
	}
	raw := ma.GetModuleMeta(scope.Meta, "memory_bootstrap_at")
	if raw == nil {
		return true
	}
	return toInt64(raw) == 0
}

func (i *Integration) resolveMemoryBootstrapPaths(_ iruntime.PromptScope) []string {
	ah, ok := i.host.(iruntime.AgentHelper)
	if !ok {
		return nil
	}
	_, loc := ah.UserTimezone()
	if loc == nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	return []string{
		fmt.Sprintf("memory/%s.md", today),
		fmt.Sprintf("memory/%s.md", yesterday),
	}
}

func (i *Integration) markMemoryPromptBootstrapped(ctx context.Context, scope iruntime.PromptScope) {
	ma, ok := i.host.(iruntime.MetadataAccess)
	if !ok || scope.Portal == nil || scope.Meta == nil {
		return
	}
	ma.SetModuleMeta(scope.Meta, "memory_bootstrap_at", time.Now().UnixMilli())
	pm, ok := i.host.(iruntime.PortalManager)
	if !ok {
		return
	}
	_ = pm.SavePortal(ctx, scope.Portal, "memory bootstrap")
}

func (i *Integration) readMemoryPromptSection(ctx context.Context, scope iruntime.PromptScope, path string) string {
	tfh, ok := i.host.(iruntime.TextFileHelper)
	if !ok {
		return ""
	}
	agentID := ""
	if ma, ok := i.host.(iruntime.MetadataAccess); ok && scope.Meta != nil {
		agentID = ma.AgentIDFromMeta(scope.Meta)
	}
	content, filePath, found, err := tfh.ReadTextFile(ctx, agentID, path)
	if err != nil || !found {
		return ""
	}
	content = normalizeNewlines(content)
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
	if strings.TrimSpace(filePath) != "" {
		return fmt.Sprintf("## %s\n%s", filePath, text)
	}
	return fmt.Sprintf("## %s\n%s", path, text)
}

// ---- private: memory manager access ----

func (i *Integration) resolveMemorySearchConfig(agentID string) *ResolvedConfig {
	cl := i.host.ConfigLookup()
	if cl == nil {
		return nil
	}
	cfg := cl.ModuleConfig("memory_search")
	agentCfg := cl.AgentModuleConfig(agentID, "memory_search")
	resolved, err := resolveMemorySearchConfigFromMaps(cfg, agentCfg)
	if err != nil {
		return nil
	}
	return resolved
}

func (i *Integration) getManager(agentID string) (Manager, string) {
	rt := i.buildRuntime()
	if rt == nil {
		return nil, "memory search unavailable"
	}
	manager, errMsg := GetMemorySearchManager(rt, agentID)
	if manager == nil {
		if errMsg == "" {
			errMsg = "memory search unavailable"
		}
		return nil, errMsg
	}
	return manager, ""
}

func (i *Integration) buildRuntime() Runtime {
	dba := i.host.DBAccess()
	if dba == nil {
		return nil
	}
	return &hostRuntimeAdapter{host: i.host, dba: dba}
}

func (i *Integration) runFlushToolLoop(
	ctx context.Context,
	portal any,
	meta any,
	model string,
	messages []openai.ChatCompletionMessageParamUnion,
) (bool, error) {
	tph, ok := i.host.(iruntime.ToolPolicyHelper)
	if !ok {
		return false, nil
	}
	allTools := tph.AllToolDefinitions()
	var flushTools []iruntime.ToolDefinition
	for _, tool := range allTools {
		if isAllowedFlushTool(tool.Name) {
			flushTools = append(flushTools, tool)
		}
	}
	if len(flushTools) == 0 {
		return false, nil
	}
	toolParams := tph.ToolsToOpenAIParams(flushTools)

	capi, ok := i.host.(iruntime.ChatCompletionAPI)
	if !ok {
		return false, nil
	}

	if err := RunFlushToolLoop(ctx, model, messages, FlushToolLoopDeps{
		TimeoutMs: int64((2 * time.Minute) / time.Millisecond),
		MaxTurns:  6,
		NextTurn: func(ctx context.Context, model string, messages []openai.ChatCompletionMessageParamUnion) (
			openai.ChatCompletionMessageParamUnion,
			[]ModelToolCall,
			bool,
			error,
		) {
			result, err := capi.NewCompletion(ctx, model, messages, toolParams)
			if err != nil {
				return openai.ChatCompletionMessageParamUnion{}, nil, false, err
			}
			if result == nil || result.Done {
				return openai.ChatCompletionMessageParamUnion{}, nil, true, nil
			}
			calls := make([]ModelToolCall, 0, len(result.ToolCalls))
			for _, tc := range result.ToolCalls {
				calls = append(calls, ModelToolCall{
					ID:       tc.ID,
					Name:     strings.TrimSpace(tc.Name),
					ArgsJSON: tc.ArgsJSON,
				})
			}
			return result.AssistantMessage, calls, len(calls) == 0, nil
		},
		ExecuteTool: func(ctx context.Context, name string, argsJSON string) (string, error) {
			if !tph.IsToolEnabled(meta, name) {
				return "", fmt.Errorf("tool %s is disabled", name)
			}
			return tph.ExecuteToolInContext(ctx, portal, meta, name, argsJSON)
		},
		OnToolError: func(name string, err error) {
			i.host.Logger().Warn("overflow flush tool failed", map[string]any{"tool": name, "error": err.Error()})
		},
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (i *Integration) resolveOverflowFlushSettings() *FlushSettings {
	oh, ok := i.host.(iruntime.OverflowHelper)
	if !ok {
		return nil
	}
	enabled, softThresholdTokens, prompt, systemPrompt := oh.OverflowFlushConfig()
	silentToken := oh.SilentReplyToken()
	defaultPrompt, defaultSystemPrompt := defaultFlushPrompts(silentToken)
	return normalizeFlushSettings(
		enabled,
		softThresholdTokens,
		prompt,
		systemPrompt,
		defaultPrompt,
		defaultSystemPrompt,
		silentToken,
	)
}

// ---- private: citations ----

func (i *Integration) resolveMemoryCitationsMode() string {
	cl := i.host.ConfigLookup()
	if cl == nil {
		return "auto"
	}
	cfg := cl.ModuleConfig(moduleName)
	if cfg == nil {
		return "auto"
	}
	raw, _ := cfg["citations"].(string)
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "on", "off", "auto":
		return mode
	default:
		return "auto"
	}
}

func (i *Integration) shouldIncludeMemoryCitations(ctx context.Context, scope iruntime.ToolScope, mode string) bool {
	switch mode {
	case "on":
		return true
	case "off":
		return false
	}
	// auto: exclude citations in group chats
	ma, ok := i.host.(iruntime.MetadataAccess)
	if !ok || scope.Portal == nil {
		return true
	}
	return !ma.IsGroupChat(ctx, scope.Portal)
}

// ---- private: memory command file write ----

func (i *Integration) writeMemoryCommandFile(
	ctx context.Context,
	scope iruntime.CommandScope,
	mode string,
	path string,
	content string,
	maxBytes int,
) (string, error) {
	tfh, ok := i.host.(iruntime.TextFileHelper)
	if !ok {
		return "", fmt.Errorf("memory storage unavailable")
	}
	agentID := ""
	if ma, ok := i.host.(iruntime.MetadataAccess); ok && scope.Meta != nil {
		agentID = ma.AgentIDFromMeta(scope.Meta)
	}
	return tfh.WriteTextFile(ctx, scope.Portal, scope.Meta, agentID, mode, path, content, maxBytes)
}

// ---- private: helpers ----

func (i *Integration) agentIDFromEventMeta(meta any) string {
	ma, ok := i.host.(iruntime.MetadataAccess)
	rawAgentID := ""
	if ok && meta != nil {
		rawAgentID = ma.AgentIDFromMeta(meta)
	}
	ah, ok := i.host.(iruntime.AgentHelper)
	if !ok {
		return strings.TrimSpace(rawAgentID)
	}
	return ah.ResolveAgentID(rawAgentID, ah.DefaultAgentID())
}

func (i *Integration) resolveBridgeDB() *dbutil.Database {
	if dba := i.host.DBAccess(); dba != nil {
		db, _ := dba.BridgeDB().(*dbutil.Database)
		return db
	}
	return nil
}

// splitQuotedArgs parses a raw argument string into tokens, respecting quoted segments.
func splitQuotedArgs(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	for _, r := range input {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote")
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args, nil
}

// ---- hostRuntimeAdapter: bridges iruntime.Host → memory.Runtime ----

type hostRuntimeAdapter struct {
	host iruntime.Host
	dba  iruntime.DBAccess
}

func (a *hostRuntimeAdapter) ResolveConfig(agentID string) (*ResolvedConfig, error) {
	cl := a.host.ConfigLookup()
	if cl == nil {
		return nil, fmt.Errorf("memory search disabled")
	}
	// Resolve memory_search config from module config + agent overrides.
	cfg := cl.ModuleConfig("memory_search")
	agentCfg := cl.AgentModuleConfig(agentID, "memory_search")
	return resolveMemorySearchConfigFromMaps(cfg, agentCfg)
}

func (a *hostRuntimeAdapter) ResolvePromptWorkspaceDir() string {
	pc := a.host.PromptContext()
	if pc == nil {
		return ""
	}
	return pc.ResolveWorkspaceDir()
}

func (a *hostRuntimeAdapter) ListSessionPortals(ctx context.Context, loginID, agentID string) ([]SessionPortal, error) {
	lh, ok := a.host.(iruntime.LoginHelper)
	if !ok {
		return nil, nil
	}
	infos, err := lh.SessionPortals(ctx, loginID, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionPortal, 0, len(infos))
	for _, info := range infos {
		portalKey, ok := info.PortalKey.(networkid.PortalKey)
		if !ok {
			continue
		}
		out = append(out, SessionPortal{Key: info.Key, PortalKey: portalKey})
	}
	return out, nil
}

func (a *hostRuntimeAdapter) BridgeDB() *dbutil.Database {
	raw := a.dba.BridgeDB()
	if raw == nil {
		return nil
	}
	db, _ := raw.(*dbutil.Database)
	return db
}

func (a *hostRuntimeAdapter) BridgeID() string {
	return a.dba.BridgeID()
}

func (a *hostRuntimeAdapter) LoginID() string {
	return a.dba.LoginID()
}

func (a *hostRuntimeAdapter) Logger() zerolog.Logger {
	return iruntime.ZerologFromHost(a.host)
}

// ---- private: config resolution ----

// resolveMemorySearchConfigFromMaps converts generic map[string]any config
// (from ConfigLookup) to agents.MemorySearchConfig and merges defaults with
// agent-specific overrides.
func resolveMemorySearchConfigFromMaps(defaults map[string]any, agentOverrides map[string]any) (*ResolvedConfig, error) {
	var defaultsCfg *agents.MemorySearchConfig
	if len(defaults) > 0 {
		cfg, err := mapToMemorySearchConfig(defaults)
		if err == nil {
			defaultsCfg = cfg
		}
	}
	var overridesCfg *agents.MemorySearchConfig
	if len(agentOverrides) > 0 {
		cfg, err := mapToMemorySearchConfig(agentOverrides)
		if err == nil {
			overridesCfg = cfg
		}
	}
	resolved := MergeSearchConfig(defaultsCfg, overridesCfg)
	if resolved == nil {
		return nil, fmt.Errorf("memory search disabled")
	}
	return resolved, nil
}

func mapToMemorySearchConfig(m map[string]any) (*agents.MemorySearchConfig, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out agents.MemorySearchConfig
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
