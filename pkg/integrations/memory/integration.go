package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"go.mau.fi/util/dbutil"

	"github.com/beeper/agentremote/pkg/agents"
	iruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
	memorycore "github.com/beeper/agentremote/pkg/memory"
	"github.com/beeper/agentremote/pkg/shared/toolspec"
	"github.com/beeper/agentremote/pkg/textfs"
)

const moduleName = "memory"

type SearchOptions = memorycore.SearchOptions
type SearchResult = memorycore.SearchResult
type FallbackStatus = memorycore.FallbackStatus
type ProviderStatus = memorycore.ProviderStatus
type ResolvedConfig = memorycore.ResolvedConfig

// Integration is the self-owned memory integration module.
// It implements ToolIntegration, PromptIntegration, CommandIntegration,
// EventIntegration, LoginPurgeIntegration, and LoginLifecycleIntegration
// directly, wiring all deps from Host
// capability interfaces.
type Integration struct {
	host iruntime.Host
}

func New(host iruntime.Host) iruntime.ModuleHooks {
	return iruntime.ModuleOrNil(host, func(host iruntime.Host) *Integration {
		return &Integration{host: host}
	})
}

func (i *Integration) Name() string { return moduleName }

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
	if !iruntime.MatchesAnyName(call.Name, "memory_search", "memory_get") {
		return false, "", nil
	}
	return ExecuteTool(ctx, call, i.buildToolExecDeps())
}

func (i *Integration) ToolAvailability(_ context.Context, scope iruntime.ToolScope, toolName string) (bool, bool, iruntime.SettingSource, string) {
	if !iruntime.MatchesAnyName(toolName, "memory_search", "memory_get") {
		return false, false, iruntime.SourceGlobalDefault, ""
	}
	if scope.Meta != nil {
		agentID := i.host.AgentIDFromMeta(scope.Meta)
		_, errMsg := i.getManager(agentID)
		if errMsg != "" {
			return true, false, iruntime.SourceProviderLimit, errMsg
		}
	}
	return true, true, iruntime.SourceGlobalDefault, ""
}

func (i *Integration) AdditionalSystemMessages(_ context.Context, _ iruntime.PromptScope) []openai.ChatCompletionMessageParamUnion {
	return nil
}

func (i *Integration) AugmentPrompt(ctx context.Context, scope iruntime.PromptScope, prompt []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
	return AugmentPrompt(ctx, scope, prompt, PromptAugmentDeps{
		ShouldInjectContext:   i.shouldInjectMemoryPromptContext,
		ShouldBootstrap:       i.shouldBootstrapMemoryPromptContext,
		ResolveBootstrapPaths: i.resolveMemoryBootstrapPaths,
		MarkBootstrapped:      i.markMemoryPromptBootstrapped,
		ReadSection:           i.readMemoryPromptSection,
	})
}

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
	if !iruntime.MatchesName(call.Name, moduleName) {
		return false, nil
	}
	return ExecuteCommand(ctx, call, i.buildCommandExecDeps())
}

func (i *Integration) OnSessionMutation(ctx context.Context, evt iruntime.SessionMutationEvent) {
	agentID := i.agentIDFromEventMeta(evt.Meta)
	manager, _ := i.getManager(agentID)
	if manager == nil {
		return
	}
	manager.NotifySessionChanged(ctx, evt.SessionKey, evt.Force)
}

func (i *Integration) OnFileChanged(_ context.Context, evt iruntime.FileChangedEvent) {
	agentID := i.agentIDFromEventMeta(evt.Meta)
	manager, _ := i.getManager(agentID)
	if manager == nil {
		return
	}
	manager.NotifyFileChanged(evt.Path)
}

func (i *Integration) OnContextOverflow(ctx context.Context, call iruntime.ContextOverflowCall) {
	HandleOverflow(ctx, call, call.Prompt, i.buildOverflowDeps())
}

func (i *Integration) OnCompactionLifecycle(ctx context.Context, evt iruntime.CompactionLifecycleEvent) {
	if evt.Meta == nil {
		return
	}
	switch evt.Phase {
	case iruntime.CompactionLifecycleStart:
		i.host.SetModuleMeta(evt.Meta, "compaction_in_flight", true)
	case iruntime.CompactionLifecycleEnd:
		i.host.SetModuleMeta(evt.Meta, "compaction_in_flight", false)
		i.host.SetModuleMeta(evt.Meta, "last_compaction_at", time.Now().UnixMilli())
		i.host.SetModuleMeta(evt.Meta, "last_compaction_dropped_count", evt.DroppedCount)
	case iruntime.CompactionLifecycleFail:
		i.host.SetModuleMeta(evt.Meta, "compaction_in_flight", false)
		i.host.SetModuleMeta(evt.Meta, "last_compaction_error", strings.TrimSpace(evt.Error))
	case iruntime.CompactionLifecycleRefresh:
		i.host.SetModuleMeta(evt.Meta, "last_compaction_refresh_at", time.Now().UnixMilli())
	}
	if evt.Portal == nil {
		return
	}
	if err := i.host.SavePortal(ctx, evt.Portal, "compaction lifecycle"); err != nil {
		i.host.Logger().Warn("failed to persist compaction lifecycle metadata", map[string]any{
			"error": err.Error(),
			"phase": string(evt.Phase),
		})
	}
}

func (i *Integration) StopForLogin(bridgeID, loginID string) {
	StopManagersForLogin(bridgeID, loginID)
}

func (i *Integration) PurgeForLogin(ctx context.Context, scope iruntime.LoginScope) error {
	db := i.resolveBridgeDB()
	if db == nil {
		return nil
	}
	StopManagersForLogin(scope.BridgeID, scope.LoginID)
	PurgeTablesBestEffort(ctx, db, scope.BridgeID, scope.LoginID)
	return nil
}

func (i *Integration) managerForScope(scope iruntime.ToolScope) (execManager, string) {
	agentID := i.agentIDFromEventMeta(scope.Meta)
	return i.getManager(agentID)
}

func (i *Integration) sessionKeyForScope(scope iruntime.ToolScope) string {
	if scope.Portal == nil {
		return ""
	}
	return i.host.PortalKeyString(scope.Portal)
}

func (i *Integration) buildToolExecDeps() ToolExecDeps {
	return ToolExecDeps{
		GetManager:             i.managerForScope,
		ResolveSessionKey:      i.sessionKeyForScope,
		ResolveCitationsMode:   func(_ iruntime.ToolScope) string { return i.resolveMemoryCitationsMode() },
		ShouldIncludeCitations: i.shouldIncludeMemoryCitations,
	}
}

func (i *Integration) buildCommandExecDeps() CommandExecDeps {
	return CommandExecDeps{
		GetManager:        i.managerForScope,
		ResolveSessionKey: i.sessionKeyForScope,
		SplitQuotedArgs:   splitQuotedArgs,
		WriteFile:         i.writeMemoryCommandFile,
	}
}

func asOverflowCall(call any) (iruntime.ContextOverflowCall, bool) {
	oc, ok := call.(iruntime.ContextOverflowCall)
	return oc, ok
}

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
		ResolveSettings: i.resolveOverflowFlushSettings,
		TrimPrompt: func(prompt []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
			return i.host.SmartTruncatePrompt(prompt, 0.5)
		},
		ContextWindow: func(call any) int {
			oc, ok := asOverflowCall(call)
			if !ok {
				return 128000
			}
			return i.host.ContextWindow(oc.Meta)
		},
		ReserveTokens: func() int {
			return i.host.CompactorReserveTokens()
		},
		EffectiveModel: func(call any) string {
			oc, ok := asOverflowCall(call)
			if !ok {
				return ""
			}
			return i.host.EffectiveModel(oc.Meta)
		},
		EstimateTokens: func(prompt []openai.ChatCompletionMessageParamUnion, model string) int {
			return i.host.EstimateTokens(prompt, model)
		},
		AlreadyFlushed: func(call any) bool {
			oc, ok := asOverflowCall(call)
			if !ok {
				return false
			}
			flushAtMs := toInt64(i.host.GetModuleMeta(oc.Meta, "overflow_flush_at"))
			if flushAtMs == 0 {
				return false
			}
			flushCC := toInt64(i.host.GetModuleMeta(oc.Meta, "overflow_flush_compaction_count"))
			return int(flushCC) == i.host.CompactionCount(oc.Meta)
		},
		MarkFlushed: func(ctx context.Context, call any) {
			oc, _ := asOverflowCall(call)
			if oc.Portal == nil || oc.Meta == nil {
				return
			}
			i.host.SetModuleMeta(oc.Meta, "overflow_flush_at", time.Now().UnixMilli())
			i.host.SetModuleMeta(oc.Meta, "overflow_flush_compaction_count", i.host.CompactionCount(oc.Meta))
			_ = i.host.SavePortal(ctx, oc.Portal, "overflow flush")
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

func (i *Integration) shouldInjectMemoryPromptContext(scope iruntime.PromptScope) bool {
	if cfg := i.host.ModuleConfig(moduleName); cfg != nil {
		inject, _ := cfg["inject_context"].(bool)
		return inject
	}
	return false
}

func (i *Integration) shouldBootstrapMemoryPromptContext(scope iruntime.PromptScope) bool {
	raw := i.host.GetModuleMeta(scope.Meta, "memory_bootstrap_at")
	if raw == nil {
		return true
	}
	return toInt64(raw) == 0
}

func (i *Integration) resolveMemoryBootstrapPaths(_ iruntime.PromptScope) []string {
	_, loc := i.host.UserTimezone()
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
	if scope.Portal == nil || scope.Meta == nil {
		return
	}
	i.host.SetModuleMeta(scope.Meta, "memory_bootstrap_at", time.Now().UnixMilli())
	_ = i.host.SavePortal(ctx, scope.Portal, "memory bootstrap")
}

func (i *Integration) readMemoryPromptSection(ctx context.Context, scope iruntime.PromptScope, path string) string {
	agentID := ""
	if scope.Meta != nil {
		agentID = i.host.AgentIDFromMeta(scope.Meta)
	}
	content, filePath, found, err := i.host.ReadTextFile(ctx, agentID, path)
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
	heading := filePath
	if strings.TrimSpace(heading) == "" {
		heading = path
	}
	return fmt.Sprintf("## %s\n%s", heading, text)
}

func (i *Integration) getManager(agentID string) (*MemorySearchManager, string) {
	manager, errMsg := GetMemorySearchManager(i.host, agentID)
	if manager == nil {
		if errMsg == "" {
			errMsg = "memory search unavailable"
		}
		return nil, errMsg
	}
	return manager, ""
}

func (i *Integration) runFlushToolLoop(
	ctx context.Context,
	portal any,
	meta any,
	model string,
	messages []openai.ChatCompletionMessageParamUnion,
) (bool, error) {
	allTools := i.host.AllToolDefinitions()
	var flushTools []iruntime.ToolDefinition
	for _, tool := range allTools {
		if isAllowedFlushTool(tool.Name) {
			flushTools = append(flushTools, tool)
		}
	}
	if len(flushTools) == 0 {
		return false, nil
	}
	toolParams := i.host.ToolsToOpenAIParams(flushTools)

	if err := RunFlushToolLoop(ctx, model, messages, FlushToolLoopDeps{
		TimeoutMs: int64((2 * time.Minute) / time.Millisecond),
		MaxTurns:  6,
		NextTurn: func(ctx context.Context, model string, messages []openai.ChatCompletionMessageParamUnion) (
			openai.ChatCompletionMessageParamUnion,
			[]ModelToolCall,
			bool,
			error,
		) {
			result, err := i.host.NewCompletion(ctx, model, messages, toolParams)
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
			if !i.host.IsToolEnabled(meta, name) {
				return "", fmt.Errorf("tool %s is disabled", name)
			}
			return i.host.ExecuteToolInContext(ctx, portal, meta, name, argsJSON)
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
	enabled, softThresholdTokens, prompt, systemPrompt := i.host.OverflowFlushConfig()
	silentToken := i.host.SilentReplyToken()
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

func (i *Integration) resolveMemoryCitationsMode() string {
	if cfg := i.host.ModuleConfig(moduleName); cfg != nil {
		raw, _ := cfg["citations"].(string)
		return normalizeCitationsMode(raw)
	}
	return "auto"
}

func (i *Integration) shouldIncludeMemoryCitations(ctx context.Context, scope iruntime.ToolScope, mode string) bool {
	switch mode {
	case "on":
		return true
	case "off":
		return false
	}
	if scope.Portal == nil {
		return true
	}
	return !i.host.IsGroupChat(ctx, scope.Portal)
}

func (i *Integration) writeMemoryCommandFile(
	ctx context.Context,
	scope iruntime.CommandScope,
	mode string,
	path string,
	content string,
	maxBytes int,
) (string, error) {
	agentID := ""
	if scope.Meta != nil {
		agentID = i.host.AgentIDFromMeta(scope.Meta)
	}
	return i.host.WriteTextFile(ctx, scope.Portal, scope.Meta, agentID, mode, path, content, maxBytes)
}

func (i *Integration) agentIDFromEventMeta(meta any) string {
	var rawAgentID string
	if meta != nil {
		rawAgentID = i.host.AgentIDFromMeta(meta)
	}
	return i.host.ResolveAgentID(rawAgentID, i.host.DefaultAgentID())
}

func (i *Integration) resolveBridgeDB() *dbutil.Database {
	raw := i.host.BridgeDB()
	if raw == nil {
		return nil
	}
	db, _ := raw.(*dbutil.Database)
	return db
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
